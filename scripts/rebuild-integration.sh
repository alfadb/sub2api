#!/usr/bin/env bash
set -euo pipefail

# 自动枚举待合并的本地分支：与 main 有共同祖先、且有 main 之外的新 commit
# 排除规则：main、priv-infra、integration*（含 integration-old）
#
# 注意：不能用 `git merge-base --is-ancestor main <branch>`，这会要求分支必须
# 直接基于 _当前_ main（即 main 是分支祖先）。upstream 同步后新 main 通常不是
# 旧 fix 分支的祖先（除非显式 rebase），会导致 fix 分支被漏扫。
# 正确做法：`git merge-base main <branch>` 只要求存在公共祖先，这在所有 fork
# 分支场景下都成立。
EXCLUDED_PATTERN='^(main|priv-infra|integration.*)$'

echo "🔍 扫描待合并分支..."
PENDING_BRANCHES=()
for branch in $(git for-each-ref --format='%(refname:short)' refs/heads/ | sort); do
    # 跳过排除的分支
    if [[ "$branch" =~ $EXCLUDED_PATTERN ]]; then
        continue
    fi
    # 检查是否与 main 有共同祖先（即属于同一仓库的分支图）
    if git merge-base main "$branch" >/dev/null 2>&1; then
        # 检查是否有 main 之外的新 commit
        if [ "$(git rev-list main.."$branch" --count)" -gt 0 ]; then
            PENDING_BRANCHES+=("$branch")
        fi
    fi
done

if [ ${#PENDING_BRANCHES[@]} -gt 0 ]; then
    echo "📋 待合并分支: ${PENDING_BRANCHES[*]}"
else
    echo "📋 无待合并分支，直接合并 priv-infra"
fi

# 预检：确认 priv-infra 存在
git rev-parse --verify priv-infra >/dev/null 2>&1 || { echo "❌ 分支 priv-infra 不存在"; exit 1; }

# 1. 重命名旧 integration（保留用于回滚）
git branch -m integration integration-old

# 2. 从 main 重建
git checkout -b integration main

# 3. 合并待上游分支
for branch in "${PENDING_BRANCHES[@]}"; do
    echo "🔀 合并 $branch ..."
    git merge "$branch" --no-edit || { echo "❌ 合并 $branch 冲突，手动解决后 git merge --continue"; exit 1; }
done

# 4. 合并私有基础设施层（.gitlab-ci.yml + scripts + 清理迁移）
echo "🔀 合并 priv-infra ..."
git merge priv-infra --no-edit || { echo "❌ 合并 priv-infra 冲突，手动解决后 git merge --continue"; exit 1; }

# 5. 验证构建
echo "🔨 验证后端构建..."
(cd backend && go build ./cmd/server/) || { echo "❌ 后端构建失败"; exit 1; }
echo "🔨 验证前端构建..."
(cd frontend && pnpm build) || { echo "❌ 前端构建失败"; exit 1; }

# 6. 清理（构建验证通过后才清理）
git branch -D integration-old
echo ""

# 7. 推送（不自动执行）
echo "✅ 重建完成，构建验证通过。确认无误后执行："
echo "  git push origin integration --force && git push -u priv integration --force"
