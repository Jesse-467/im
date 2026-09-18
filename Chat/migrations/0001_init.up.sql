-- ============================================================================
-- chat 库 · 初始化
--
-- 说明：库 im_chat 由 deploy/init/mysql/01-create-databases.sql 负责创建，
-- 本脚本只建表。
-- ============================================================================

-- ---------------------------------------------------------------------------
-- 会话：单聊与群聊统一建模
--
-- 之所以不拆成两张表：消息、成员、已读位点等逻辑对两者完全一致，
-- 用同一个会话实体可以让消息链路只维护一条代码路径，群聊的额外属性
-- 通过 extra 字段承载，避免为一小部分差异付出双倍维护成本。
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS `conversation` (
    `id`         BIGINT UNSIGNED NOT NULL                COMMENT '会话 ID（雪花 ID）',
    `type`       TINYINT         NOT NULL                COMMENT '1 单聊 2 群聊',
    `biz_key`    VARCHAR(191)    NOT NULL                COMMENT '业务唯一键：单聊为 minUid_maxUid，群聊为雪花串',
    `name`       VARCHAR(191)    NOT NULL DEFAULT ''     COMMENT '会话名称（群名）',
    `avatar_url` VARCHAR(512)    NOT NULL DEFAULT ''     COMMENT '会话头像',
    `status`     TINYINT         NOT NULL DEFAULT 1      COMMENT '1 正常 2 待确认 3 拉黑',
    `owner_id`   BIGINT UNSIGNED NOT NULL DEFAULT 0      COMMENT '群主用户 ID，单聊为 0',
    `max_seq`    BIGINT          NOT NULL DEFAULT 0      COMMENT '会话内最新消息序号',
    `extra`      JSON            NULL                    COMMENT '扩展属性（如群公告等）',
    `created_at` DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    `updated_at` DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (`id`),
    -- (type, biz_key) 唯一：单聊保证同一对用户只会有一个会话，
    -- 群聊保证同一个雪花串不会重复建群，并发下由数据库兜底幂等。
    UNIQUE KEY `uk_type_bizkey` (`type`, `biz_key`),
    KEY `idx_owner` (`owner_id`)
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_0900_ai_ci COMMENT = '会话表';

-- ---------------------------------------------------------------------------
-- 会话成员
--
-- last_read_seq 记录成员各自读到哪一条，未读数 = conversation.max_seq - last_read_seq，
-- 避免为每个成员维护一条未读计数（写扩散）而付出高昂的写放大代价。
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS `conversation_member` (
    `id`             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '自增主键',
    `conversation_id` BIGINT UNSIGNED NOT NULL               COMMENT '会话 ID',
    `user_id`        BIGINT UNSIGNED NOT NULL                COMMENT '成员用户 ID',
    `alias_name`     VARCHAR(191)    NOT NULL DEFAULT ''     COMMENT '成员在会话内的昵称',
    `role`           TINYINT         NOT NULL DEFAULT 0      COMMENT '0 成员 1 管理员 2 群主',
    `last_read_seq`  BIGINT          NOT NULL DEFAULT 0      COMMENT '该成员已读到的最大序号',
    `mute`           TINYINT         NOT NULL DEFAULT 0      COMMENT '0 正常 1 免打扰',
    `joined_at`      DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3) COMMENT '加入时间',
    PRIMARY KEY (`id`),
    -- 同一会话内一个用户只能有一条成员记录，杜绝重复入群导致的消息重复投递
    UNIQUE KEY `uk_conv_user` (`conversation_id`, `user_id`),
    -- 支撑「我参与的会话列表」这一最高频查询
    KEY `idx_user_conv` (`user_id`, `conversation_id`)
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_0900_ai_ci COMMENT = '会话成员表';

-- ---------------------------------------------------------------------------
-- 消息
--
-- 用 (conversation_id, seq) 而非时间戳做排序权威：分布式环境下各机时钟不可信，
-- 会话内单调递增的 seq 才是可靠的顺序依据；client_msg_id 作为幂等键，
-- 保证客户端重试不会产生重复消息。
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS `message` (
    `id`              BIGINT UNSIGNED NOT NULL                COMMENT '消息 ID（雪花 ID）',
    `conversation_id` BIGINT UNSIGNED NOT NULL                COMMENT '会话 ID',
    `seq`             BIGINT          NOT NULL                COMMENT '会话内单调递增序号，顺序权威',
    `sender_id`       BIGINT UNSIGNED NOT NULL                COMMENT '发送者用户 ID',
    `type`            TINYINT         NOT NULL DEFAULT 1      COMMENT '1 文本 2 图片 3 视频 4 音频 5 系统',
    `content`         TEXT            NULL                    COMMENT '消息内容',
    `extra`           JSON            NULL                    COMMENT '扩展属性（如图片尺寸、@ 列表）',
    `client_msg_id`   VARCHAR(64)     NOT NULL                COMMENT '客户端消息 ID，用作幂等键',
    `status`          TINYINT         NOT NULL DEFAULT 1      COMMENT '1 正常 2 撤回 3 删除',
    `created_at`      DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (`id`),
    -- (conversation_id, seq) 唯一：seq 分配若出现并发冲突，写入会失败，
    -- 从而保证「同会话内序号绝不重复」这一拉取模型的根基不被破坏。
    UNIQUE KEY `uk_conv_seq` (`conversation_id`, `seq`),
    -- 幂等兜底：同一发送者携带相同 client_msg_id 的重试只会落库一次
    UNIQUE KEY `uk_conv_clientmsg` (`conversation_id`, `sender_id`, `client_msg_id`),
    -- 支撑按会话倒序拉取历史消息
    KEY `idx_conv_created` (`conversation_id`, `created_at`)
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_0900_ai_ci COMMENT = '消息表';

-- ---------------------------------------------------------------------------
-- 好友关系
--
-- 双向落库（A→B 与 B→A 各一条）：读场景（我的好友列表）只需单表查询，
-- 无需 OR 条件扫描，用一份可控的写冗余换取读路径的简洁与索引高效。
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS `friend_relation` (
    `id`         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '自增主键',
    `user_id`    BIGINT UNSIGNED NOT NULL                COMMENT '用户 ID',
    `friend_id`  BIGINT UNSIGNED NOT NULL                COMMENT '好友用户 ID',
    `remark`     VARCHAR(191)    NOT NULL DEFAULT ''     COMMENT '好友备注名',
    `status`     TINYINT         NOT NULL DEFAULT 1      COMMENT '1 正常 2 拉黑',
    `created_at` DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (`id`),
    -- 同一对好友关系只允许一条，防止重复添加
    UNIQUE KEY `uk_user_friend` (`user_id`, `friend_id`),
    -- 支撑反向查询（谁把我加为好友）
    KEY `idx_friend` (`friend_id`)
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_0900_ai_ci COMMENT = '好友关系表';

-- ---------------------------------------------------------------------------
-- 好友申请
--
-- expire_at 参与唯一键，使「同一对用户的历史申请」因状态/时间不同而自然共存，
-- 同时防止重复的待处理申请堆积。
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS `friend_request` (
    `id`         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '自增主键',
    `from_uid`   BIGINT UNSIGNED NOT NULL                COMMENT '申请人用户 ID',
    `to_uid`     BIGINT UNSIGNED NOT NULL                COMMENT '被申请人用户 ID',
    `apply_msg`  VARCHAR(255)    NOT NULL DEFAULT ''     COMMENT '申请附言',
    `status`     TINYINT         NOT NULL DEFAULT 0      COMMENT '0 待处理 1 已同意 2 已拒绝 3 已过期',
    `expire_at`  DATETIME(3)     NOT NULL                COMMENT '申请过期时间',
    `handled_at` DATETIME(3)     NULL                    COMMENT '处理时间',
    `created_at` DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (`id`),
    UNIQUE KEY `uk_from_to_status` (`from_uid`, `to_uid`, `status`),
    -- 支撑「我的待处理申请」拉取
    KEY `idx_to_status` (`to_uid`, `status`)
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_0900_ai_ci COMMENT = '好友申请表';

-- ---------------------------------------------------------------------------
-- 本地消息表（Outbox）
--
-- 消息落库与投递到 MQ 无法共用一个事务，若先落库再投递则可能丢投递。
-- 因此把「待投递事件」与业务数据写在同一事务里，再由后台任务扫描投递，
-- 保证「落库必投递」；消费端依赖 event_id 做幂等，容忍重复投递。
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS `message_outbox` (
    `id`            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '自增主键',
    `event_id`      VARCHAR(64)     NOT NULL                COMMENT '事件 ID，消费端据此幂等',
    `topic`         VARCHAR(64)     NOT NULL                COMMENT '目标 topic',
    `partition_key` VARCHAR(64)     NOT NULL                COMMENT '分区键，通常为会话 ID 以保证同会话有序',
    `payload`       MEDIUMBLOB      NOT NULL                COMMENT '事件负载（序列化后的消息体）',
    `status`        TINYINT         NOT NULL DEFAULT 0      COMMENT '0 待投递 1 已投递 2 失败',
    `retry_count`   INT             NOT NULL DEFAULT 0      COMMENT '重试次数',
    `next_retry_at` DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3) COMMENT '下次重试时间',
    `created_at`    DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (`id`),
    UNIQUE KEY `uk_event` (`event_id`),
    -- 扫描任务的核心索引：按状态 + 下次重试时间取待投递记录
    KEY `idx_status_retry` (`status`, `next_retry_at`)
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_0900_ai_ci COMMENT = '本地消息表（Outbox）';
