#!/usr/bin/env bash
set -euo pipefail

# 自动枚举待合并的本地分支：基于 main 且不是 main/priv-overlay/integration 的分支
# 排除规则：main、priv-overlay、integration*（含 integration-old）
EXCLUDED_PATTERN='^(main|priv-overlay|integration.*)$'

echo "🔍 扫描待合并分支..."
PENDING_BRANCHES=()
for branch in $(git for-each-ref --format='%(refname:short)' refs/heads/ | sort); do
    # 跳过排除的分支
    if [[ "$branch" =~ $EXCLUDED_PATTERN ]]; then
        continue
    fi
    # 检查是否基于 main（main 是该分支的祖先）
    if git merge-base --is-ancestor main "$branch" 2>/dev/null; then
        # 检查是否有 main 之外的新 commit
        if [ "$(git rev-list main.."$branch" --count)" -gt 0 ]; then
            PENDING_BRANCHES+=("$branch")
        fi
    fi
done

if [ ${#PENDING_BRANCHES[@]} -gt 0 ]; then
    echo "📋 待合并分支: ${PENDING_BRANCHES[*]}"
else
    echo "📋 无待合并分支，直接合并 priv-overlay"
fi

# 预检：确认 priv-overlay 存在
git rev-parse --verify priv-overlay >/dev/null 2>&1 || { echo "❌ 分支 priv-overlay 不存在"; exit 1; }

# 1. 重命名旧 integration（保留用于回滚）
git branch -m integration integration-old

# 2. 从 main 重建
git checkout -b integration main

# 3. 合并待上游分支
for branch in "${PENDING_BRANCHES[@]}"; do
    echo "🔀 合并 $branch ..."
    git merge "$branch" --no-edit || { echo "❌ 合并 $branch 冲突，手动解决后 git merge --continue"; exit 1; }
done

# 4. 合并私有叠加层（包含 .gitlab-ci.yml）
echo "🔀 合并 priv-overlay ..."
git merge priv-overlay --no-edit || { echo "❌ 合并 priv-overlay 冲突，手动解决后 git merge --continue"; exit 1; }

# 5. 自动解决已知冲突模式：wire_gen.go
if git diff --name-only --diff-filter=U 2>/dev/null | grep -q 'wire_gen.go'; then
    echo "⚙️  自动解决 wire_gen.go 冲突..."
    git checkout --theirs backend/cmd/server/wire_gen.go 2>/dev/null || true
    (cd backend && go generate ./cmd/server/) || { echo "❌ wire_gen.go 重新生成失败"; exit 1; }
    git add backend/cmd/server/wire_gen.go
fi

# 6. 验证构建
echo "🔨 验证后端构建..."
(cd backend && go build ./cmd/server/) || { echo "❌ 后端构建失败"; exit 1; }
echo "🔨 验证前端构建..."
(cd frontend && pnpm build) || { echo "❌ 前端构建失败"; exit 1; }

# 7. 清理（构建验证通过后才清理）
git branch -D integration-old
echo ""

# 10. 推送（不自动执行）
echo "✅ 重建完成，构建验证通过。确认无误后执行："
echo "  git push origin integration --force && git push -u priv integration --force"
