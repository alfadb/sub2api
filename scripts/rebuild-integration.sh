#!/usr/bin/env bash
set -euo pipefail

# 可配置：待上游合并的分支列表
# 所有分支已上游时数组为空，直接跳到 priv-overlay 合并
PENDING_BRANCHES=()

# 预检：确认所有分支存在
for branch in "${PENDING_BRANCHES[@]}" priv-overlay; do
    git rev-parse --verify "$branch" >/dev/null 2>&1 || { echo "❌ 分支 $branch 不存在"; exit 1; }
done

# 1. 保存旧 CI commit
OLD_CI=$(git log integration --oneline -- .gitlab-ci.yml | head -1 | awk '{print $1}')

# 2. 重命名旧 integration（保留用于回滚）
git branch -m integration integration-old

# 3. 从 main 重建
git checkout -b integration main

# 4. 合并待上游分支
for branch in "${PENDING_BRANCHES[@]}"; do
    git merge "$branch" --no-edit || { echo "❌ 合并 $branch 冲突，手动解决后 git merge --continue"; exit 1; }
done

# 5. 合并私有叠加层
git merge priv-overlay --no-edit || { echo "❌ 合并 priv-overlay 冲突，手动解决后 git merge --continue"; exit 1; }

# 6. 自动解决已知冲突模式：wire_gen.go
if git diff --name-only --diff-filter=U 2>/dev/null | grep -q 'wire_gen.go'; then
    echo "⚙️  自动解决 wire_gen.go 冲突..."
    git checkout --theirs backend/cmd/server/wire_gen.go 2>/dev/null || true
    (cd backend && go generate ./cmd/server/) || { echo "❌ wire_gen.go 重新生成失败"; exit 1; }
    git add backend/cmd/server/wire_gen.go
fi

# 7. 恢复 CI 配置
git cherry-pick "$OLD_CI" || { echo "❌ cherry-pick CI 冲突，手动解决后 git cherry-pick --continue"; exit 1; }

# 8. 验证构建
echo "🔨 验证后端构建..."
(cd backend && go build ./cmd/server/) || { echo "❌ 后端构建失败"; exit 1; }
echo "🔨 验证前端构建..."
(cd frontend && pnpm build) || { echo "❌ 前端构建失败"; exit 1; }

# 9. 清理（构建验证通过后才清理）
git branch -D integration-old
echo ""

# 10. 推送（不自动执行）
echo "✅ 重建完成，构建验证通过。确认无误后执行："
echo "  git push -u priv integration --force"
