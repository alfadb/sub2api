#!/usr/bin/env bash
set -euo pipefail

# 查询并校验「GitHub Actions + GitLab CI」对某个 commit 的状态。
#
# 背景：发版前置条件要求双端 CI 对精确 SHA 全部 success（仅看对应 ref 那一行）。
# 历史上临时拼命令出过静默错误（如把 jq 的 --arg 传给 gh --jq），查询失败被
# 2>/dev/null 吞掉后输出全空、轮询永远等不到终态。本脚本把口径固化：
#   - GitHub: gh api actions/runs?head_sha=<完整SHA>，逐 run 报告 status/conclusion
#   - GitLab: glab api pipelines?sha=<完整SHA>（可用 --ref 进一步限定 ref）
#   - 查询失败（非零退出/输出不可解析）一律显式报错退出，绝不当作"已完成"
#   - 零条记录视为"未就绪"而非成功（防误判）
#
# 用法:
#   scripts/check-ci-status.sh [SHA]                 单次查询，打印状态
#   scripts/check-ci-status.sh --wait [SHA]          有界轮询至双端全部终态
#   选项:
#     --wait            轮询模式（默认单次）
#     --ref <name>      限定 GitLab pipeline 的 ref（如 integration 或 v0.9.x）
#     --max-minutes <n> 轮询上限，默认 40 分钟（保险丝，不是验收路径）
#     --interval <sec>  轮询间隔，默认 60 秒
#   SHA 缺省 = 本地 integration 分支 tip
#
# 退出码: 0=双端全部 success；1=存在失败/取消；2=仍在进行中（单次）或超时（轮询）；
#         3=查询本身出错（网络/CLI/解析）

WAIT=0
GL_REF=""
MAX_MINUTES=40
INTERVAL=60
SHA=""
while [ $# -gt 0 ]; do
    case "$1" in
        --wait) WAIT=1 ;;
        --ref) GL_REF="${2:?--ref needs a value}"; shift ;;
        --max-minutes) MAX_MINUTES="${2:?--max-minutes needs a value}"; shift ;;
        --interval) INTERVAL="${2:?--interval needs a value}"; shift ;;
        -h|--help) sed -n '2,30p' "$0"; exit 0 ;;
        *) if [ -n "$SHA" ]; then echo "❌ 多余参数: $1" >&2; exit 3; fi; SHA="$1" ;;
    esac
    shift
done

# SHA 缺省取本地 integration tip；统一解析成完整 40 位，避免前缀歧义
[ -n "$SHA" ] || SHA="integration"
if ! FULL_SHA=$(git rev-parse --verify "${SHA}^{commit}" 2>/dev/null); then
    echo "❌ 无法解析 commit: $SHA" >&2
    exit 3
fi
SHORT_SHA="${FULL_SHA:0:9}"

# 从 remote URL 推导两端坐标（origin=GitHub fork，priv=GitLab），失败即报错
GH_SLUG=$(git remote get-url origin | sed -E 's#.*github\.com[:/]##; s#\.git$##')
case "$GH_SLUG" in
    */*) : ;;
    *) echo "❌ 无法从 origin 推导 GitHub owner/repo: $GH_SLUG" >&2; exit 3 ;;
esac
GL_PATH=$(git remote get-url priv | sed -E 's#https?://[^/]+/##; s#\.git$##')
GL_SLUG=$(echo "$GL_PATH" | sed 's#/#%2F#g')

gh_run_list() {
    # 输出: 每行 "name<TAB>status<TAB>conclusion"；查询失败返回非零
    gh api "repos/${GH_SLUG}/actions/runs?head_sha=${FULL_SHA}&per_page=50" \
        --jq '.workflow_runs[] | [.name, .status, (.conclusion // "-")] | @tsv'
}

gl_pipeline_list() {
    # 输出: 每行 "id<TAB>ref<TAB>status"；查询失败返回非零
    # 注意: 本机 glab api 不支持 --jq（报 Unknown flag），JSON 一律交给 jq 解析
    local q="pipelines?sha=${FULL_SHA}"
    [ -n "$GL_REF" ] && q="${q}&ref=${GL_REF}"
    glab api "projects/${GL_SLUG}/${q}" \
        | jq -r '.[] | [(.id|tostring), .ref, .status] | @tsv'
}

check_once() {
    # 打印状态明细；设置全局 GH_OK/GL_OK（1=全部终态 success）与 ANY_FAIL（1=存在终态失败）
    local failed=0 gh_out gl_out
    ANY_FAIL=0

    if ! gh_out=$(gh_run_list); then
        echo "❌ GitHub Actions 查询失败（gh api 报错）" >&2; return 3
    fi
    if ! gl_out=$(gl_pipeline_list); then
        echo "❌ GitLab 查询失败（glab api 报错）" >&2; return 3
    fi

    echo "── GitHub Actions (${GH_SLUG}) @ ${SHORT_SHA}"
    GH_OK=1
    if [ -z "$gh_out" ]; then
        echo "   （无任何 run —— 视为未就绪）"
        GH_OK=0
    else
        while IFS=$'\t' read -r name status conclusion; do
            local mark="⏳"
            [ "$status" = "completed" ] || GH_OK=0
            if [ "$status" = "completed" ]; then
                if [ "$conclusion" = "success" ]; then mark="✅"; else mark="❌ ${conclusion}"; GH_OK=0; ANY_FAIL=1; fi
            fi
            echo "   ${mark} ${name}: ${status}/${conclusion}"
        done <<<"$gh_out"
    fi

    echo "── GitLab (${GL_PATH}) @ ${SHORT_SHA}${GL_REF:+ (ref=${GL_REF})}"
    GL_OK=1
    if [ -z "$gl_out" ]; then
        echo "   （无 pipeline —— 视为未就绪）"
        GL_OK=0
    else
        while IFS=$'\t' read -r pid ref status; do
            local mark="⏳"
            case "$status" in
                success) mark="✅" ;;
                failed|canceled|skipped) mark="❌ ${status}"; GL_OK=0; ANY_FAIL=1 ;;
                *) GL_OK=0 ;;
            esac
            echo "   ${mark} #${pid} ${ref}: ${status}"
        done <<<"$gl_out"
    fi
    return 0
}

attempt=0
max_attempts=$(( MAX_MINUTES * 60 / INTERVAL ))
while :; do
    attempt=$((attempt + 1))
    echo "═══ 第 ${attempt} 次查询 $(date '+%H:%M:%S') ═══"
    if ! check_once; then
        echo "❌ 查询出错，直接退出（不当作已完成）" >&2
        exit 3
    fi
    if [ "$GH_OK" = "1" ] && [ "$GL_OK" = "1" ]; then
        echo "✅ 双端 CI 对 ${SHORT_SHA} 全部 success"
        exit 0
    fi
    # 轮询模式：既已出现终态失败，等待无意义，立即失败退出
    if [ "$ANY_FAIL" = "1" ]; then
        echo "❌ 双端存在终态失败（见上），停止等待"
        exit 1
    fi
    # 单次模式：有终态失败 → 1；否则未全部成功 → 2
    if [ "$WAIT" = "0" ]; then
        if [ "$ANY_FAIL" = "1" ]; then
            echo "❌ 存在终态失败（见上）"
            exit 1
        fi
        echo "⏳ 尚未全部终态（单次模式）"
        exit 2
    fi
    if [ "$attempt" -ge "$max_attempts" ]; then
        echo "❌ 超时（${MAX_MINUTES} 分钟保险丝触发）：预期双端 success，观测到 GH_OK=${GH_OK} GL_OK=${GL_OK}。这不是验收通过，需人工核查。"
        exit 2
    fi
    sleep "$INTERVAL"
done
