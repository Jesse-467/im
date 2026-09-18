-- ============================================================================
-- 本地/测试环境数据库初始化（PostgreSQL 17）
--
-- 说明：数据库的创建与表的迁移刻意分离。
--   - 本脚本只负责「创建数据库」，它属于部署动作；
--   - 表结构变更由各服务自己目录下的 migrations/ 管理，随服务版本演进。
-- 这样两个服务可以独立发布，不会互相阻塞在同一个建库脚本上。
--
-- 用 \gexec 实现幂等：PostgreSQL 没有 CREATE DATABASE IF NOT EXISTS，
-- 直接 CREATE 会在重跑时报错。这里先查询再动态执行，可安全重复运行。
-- ============================================================================

SELECT format('CREATE DATABASE %I', d)
FROM (VALUES ('im_account'), ('im_chat')) AS t(d)
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = t.d)
\gexec
