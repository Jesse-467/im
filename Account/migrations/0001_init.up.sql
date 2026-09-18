-- ============================================================================
-- account 库 · 初始化
-- ============================================================================

CREATE TABLE IF NOT EXISTS `user` (
    `id`         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '用户 ID',
    `email`      VARCHAR(191)    NOT NULL                COMMENT '登录邮箱，全局唯一',
    `password`   VARCHAR(255)    NOT NULL                COMMENT 'bcrypt 哈希，禁止存明文',
    `nickname`   VARCHAR(64)     NOT NULL DEFAULT ''     COMMENT '昵称',
    `gender`     TINYINT         NOT NULL DEFAULT 0      COMMENT '0 未知 1 男 2 女',
    `avatar_url` VARCHAR(512)    NOT NULL DEFAULT ''     COMMENT '头像地址',
    `created_at` DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    `updated_at` DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (`id`),
    -- 唯一索引同时承担「邮箱不可重复」的并发兜底职责：
    -- 即使两个注册请求同时通过了前置校验，也只有一个能写入成功。
    UNIQUE KEY `uk_email` (`email`)
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_0900_ai_ci COMMENT = '账号表';
