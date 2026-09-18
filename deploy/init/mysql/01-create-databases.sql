-- ============================================================================
-- 本地/测试环境数据库初始化
--
-- 说明：数据库的创建与表的迁移刻意分离。
--   - 本脚本只负责「创建数据库」，它是部署动作，由 DBA 或部署流程执行；
--   - 表结构变更由各服务自己目录下的 migrations/ 管理，随服务版本演进。
-- 这样服务可以独立发布，而不会互相阻塞在同一个建库脚本上。
-- ============================================================================

CREATE DATABASE IF NOT EXISTS `im_account`
    DEFAULT CHARACTER SET utf8mb4
    COLLATE utf8mb4_0900_ai_ci;

CREATE DATABASE IF NOT EXISTS `im_chat`
    DEFAULT CHARACTER SET utf8mb4
    COLLATE utf8mb4_0900_ai_ci;
