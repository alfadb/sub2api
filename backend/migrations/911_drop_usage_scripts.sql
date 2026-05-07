-- +goose Up
-- 清理 priv-overlay 时代的 starlark 用量脚本表。
--
-- 背景：priv-overlay 引入了 910_add_usage_scripts.sql 来支持 Starlark 用量
-- 脚本引擎（admin UI 维护、后台定期执行采集第三方中转站用量）。该模块随
-- priv-overlay 一并废弃后，表本身不再被任何代码引用，需要显式 DROP。
--
-- 幂等性：使用 IF EXISTS 保证以下场景都安全：
--   1. priv 生产环境（已应用 910）：会真正 DROP 表与索引
--   2. 全新部署（从未应用 910）：DROP IF EXISTS 静默跳过
--   3. main / origin 部署（永远没有 910）：同上，静默跳过
--
-- 注意：schema_migrations 表中残留的 910_add_usage_scripts.sql 行属于历史
-- 足迹，runner 不会校验"DB 已应用迁移是否仍在 FS 中"，因此保留无害。
-- 如有审计洁癖需要，可由 DBA 手动 DELETE 对应行。

DROP INDEX IF EXISTS idx_usage_scripts_host_type;
DROP TABLE IF EXISTS usage_scripts;
