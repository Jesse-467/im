# IM Message —— 基于 Kratos + Gin 的即时通讯系统

一个即时通讯（IM）后端系统，提供**账号中心**与**聊天服务**两大能力：
账号注册登录、好友关系、单聊与群聊、消息实时推送与离线拉取。

系统由**三个互相独立的 Go module** 组成，除 gRPC 协议外不共享任何代码：

| 目录 | module | 职责 |
| --- | --- | --- |
| `Account/` | `github.com/Jesse-467/im/Account` | 账号中心（独立微服务应用） |
| `Chat/` | `github.com/Jesse-467/im/Chat` | 即时聊天（独立微服务应用） |
| `pkg/` | `github.com/Jesse-467/im/pkg` | **只放公共协议**（`.proto` 与生成的 `*.pb.go`） |

---

## 目录

- [1. 技术栈](#1-技术栈)
- [2. 系统架构](#2-系统架构)
- [3. 目录结构](#3-目录结构)
- [4. 接口文档](#4-接口文档)
- [5. 核心流程](#5-核心流程)
- [6. 数据模型](#6-数据模型)
- [7. 关键设计决策](#7-关键设计决策)
- [8. 配置说明](#8-配置说明)
- [9. 构建与运行](#9-构建与运行)
- [10. 部署到云服务器](#10-部署到云服务器)
- [11. 测试](#11-测试)
- [12. 容量与扩展性](#12-容量与扩展性)
- [13. 已知限制](#13-已知限制)

---

## 1. 技术栈

| 分类 | 技术 | 版本 | 用途 |
| --- | --- | --- | --- |
| 语言 | Go | 1.23.1 | 全部服务 |
| 微服务框架 | Kratos | v2.9.2 | 服务生命周期、日志、配置、传输层抽象 |
| HTTP 框架 | Gin | v1.10.0 | 业务 REST 接口、WebSocket 挂载 |
| 依赖注入 | Wire | v0.6.0 | 编译期装配，运行时零反射 |
| RPC | gRPC + Protocol Buffers | grpc 1.72.0 / protobuf 1.36.6 | Account ↔ Chat 服务间调用 |
| 实时通信 | gorilla/websocket | v1.5.3 | 客户端长连接 |
| 关系型数据库 | PostgreSQL | 17 | 全部持久化 |
| ORM | GORM | v1.25.12 | 数据访问（驱动 pgx v5） |
| 缓存 | Redis | 7.4 | 会话序号分配、在线路由 |
| 消息队列 | Kafka | 3.7 (KRaft) | 消息异步投递 |
| 认证 | golang-jwt/jwt | v5.2.2 | JWT 签发与校验 |
| 密码 | x/crypto/bcrypt | v0.38.0 | 密码哈希 |
| 日志 | zap | v1.27.0 | 结构化日志 |

> **为什么是 Kratos + Gin 而不是二选一**：Kratos 负责服务治理（生命周期、
> 优雅启停、日志、服务注册、链路追踪），Gin 负责路由与中间件生态。
> 二者通过实现 Kratos 的 `transport.Server` 接口融合 ——
> 见 [http.go](file:///d:/GoCode/IMmessage/im/Chat/internal/server/http.go#L23-L30)。
> 这样既保留了 Gin 的易用性，又能让 HTTP 服务与 gRPC、WebSocket 一起被
> `kratos.App` 统一管理启停。

---

## 2. 系统架构

### 2.1 总体结构

```
                       ┌──────────────────────┐
                       │        客户端         │
                       │  HTTP  /  WebSocket   │
                       └───┬──────────────┬────┘
                           │              │
        ┌──────────────────┴───┐     ┌────┴────────────────────────────┐
        │   Account（账号中心）  │     │   Chat（即时聊天）               │
        │   HTTP  :8001        │     │   HTTP + WS  :8002              │
        │   gRPC  :9001        │◀────┤   ↳ 经 gRPC 取用户资料/校验      │
        └──────────┬───────────┘     └────┬───────────────┬────────────┘
                   │                      │               │
        ┌──────────┴───────────┐  ┌───────┴──────┐  ┌─────┴──────┐
        │  PostgreSQL          │  │  PostgreSQL  │  │  Redis      │
        │  im_account          │  │  im_chat     │  │  序号/在线路由│
        └──────────────────────┘  └──────────────┘  └─────┬──────┘
                                                          │
                                              ┌───────────┴────────────┐
                                              │  Kafka  im.message      │
                                              └────────────────────────┘
```

### 2.2 分层（Kratos 四层）

两个服务都严格遵循 Kratos 的分层约定，依赖方向单向向下：

```
  server   ──  传输层：HTTP 路由、WebSocket 连接、gRPC 服务
     │         只负责协议适配，不含业务规则
  service  ──  适配层：参数绑定 → 调用 biz → 错误码映射 → 组装响应
     │         不含 if/else 形式的业务分支
  biz      ──  领域层：用例、实体、领域错误、仓储接口（interface 定义在这里）
     │         纯业务规则，不依赖任何框架与数据库
  data     ──  数据层：GORM 仓储实现、Redis、Kafka、外部 RPC 客户端
```

**biz 层定义接口、data 层实现**是这个架构最关键的一点：业务规则不依赖
具体存储，因此可以在没有数据库的情况下测试业务逻辑。

### 2.3 消息投递链路

```
客户端 A ──WS send──▶ Chat 网关 ──▶ MessageUseCase.Send
                                      │
                                      ├─ ① 校验成员身份
                                      ├─ ② 幂等回查（clientMsgId 命中则直接返回）
                                      ├─ ③ Redis 分配会话 seq 与 sender_seq
                                      └─ ④ 单事务写入：
                                             message + 推进 conversation.max_seq
                                             + 累加成员 unread_count
                                             + 写入 message_outbox
                                      ▼
                              Relay 协程捞取 Outbox（FOR UPDATE SKIP LOCKED）
                                      ▼
                                投递到 Kafka（key = 会话 ID，保证同会话有序）
                                      ▼
                         Consumer 消费 ──▶ 查会话成员 ──▶ 推给本节点在线连接
                                      ▼
                              客户端 B 收到 message 推送
```

**为什么用 Outbox 而不是直接投递**：直接「先写库再发 MQ」在两步之间崩溃会
丢消息；「先发 MQ 再写库」则可能推送了不存在的消息。Outbox 把事件写入与
业务写入放进**同一个数据库事务**，再由后台协程可靠搬运，从而得到
at-least-once 语义且不丢消息。

---

## 3. 目录结构

```
im/
├── Account/                     # 账号中心（独立 module）
│   ├── cmd/account/             # main / wire / wire_gen
│   ├── internal/
│   │   ├── auth/                # JWT 签发与校验
│   │   ├── biz/                 # 用户领域：bcrypt、UserRepo 接口
│   │   ├── conf/                # 环境变量装载 + 强校验
│   │   ├── data/                # GORM 仓储实现
│   │   ├── errs/                # 错误码体系
│   │   ├── server/              # HTTP（Gin）+ gRPC 传输层
│   │   └── service/             # 适配层（同时实现 HTTP 与 gRPC）
│   └── migrations/              # 建表 SQL
│
├── Chat/                        # 即时聊天（独立 module）
│   ├── cmd/chat/
│   ├── internal/
│   │   ├── biz/                 # 会话 / 消息 / 好友 / 群聊领域
│   │   ├── consumer/            # 从 MQ 消费并推送给在线用户
│   │   ├── data/                # GORM 仓储、Redis 发号、账号中心 RPC 客户端
│   │   ├── mq/                  # Publisher / Subscriber 抽象（kafka | log）
│   │   ├── relay/               # Outbox 投递协程
│   │   ├── server/              # HTTP（Gin）+ WS 传输层
│   │   ├── service/             # 适配层
│   │   ├── ws/                  # WebSocket 网关：连接管理、心跳、跨节点路由
│   │   └── xid/                 # 雪花 ID 生成器
│   └── migrations/
│
├── pkg/                         # 只放公共协议
│   ├── account/v1/account.proto
│   └── chat/v1/{message,conversation,group,friend}.proto
│
├── deploy/
│   ├── docker-compose.local.yaml    # 本地中间件编排
│   └── init/postgres/               # 数据库初始化脚本
│
├── Makefile                     # 统一入口（tidy/proto/wire/test/build/...）
├── .env.example                 # 全量环境变量模板
└── REFACTOR_PLAN.md             # 重构章程
```

---

## 4. 接口文档

### 4.1 统一响应结构

两个服务的 HTTP 接口使用同一套响应封装：

```json
{ "code": 0, "msg": "OK", "data": {} }
```

### 4.2 错误码

`code` 取值语义（Chat 与 Account 各自独立定义一份，数值保持一致，
以便客户端用同一套逻辑处理两个服务）：

| 段 | 含义 | 具体码 |
| --- | --- | --- |
| `0` | 成功 | `0` |
| `400x` | 客户端错误 | `4001` 未认证 / `4002` 参数错误 / `4003` 数据不存在 / `4004` 数据已存在 / `4005` 限流 / `4006` 无权限 |
| `500x` | 服务端错误 | `5000` 内部错误 / `5001` 数据库异常 / `5002` 缓存异常 / `5003` 序列化异常 / `5004` MQ 异常 / `5005` 未实现 |

> `CodeError` 实现了 gRPC 的 `GRPCStatus()`，因此同一个错误在 HTTP 与 gRPC
> 上保持数值码一致，无需额外转换。

### 4.3 Account（账号中心，默认 `:8001`）

| 方法 | 路径 | 鉴权 | 说明 |
| --- | --- | --- | --- |
| GET | `/healthz` | 否 | 存活探针 |
| GET | `/readyz` | 否 | 就绪探针（真实探测 DB/Redis） |
| POST | `/api/user/register` | 否 | 注册（`email` / `password` / `nickName` / `gender`） |
| POST | `/api/user/login` | 否 | 登录，返回 `userId` + `accessToken` + `accessExpire` |
| POST | `/api/user/personal_info` | 是 | 查询本人资料 |
| POST | `/api/user/query_user_info` | 是 | 按 `userId` 查询他人资料 |
| POST | `/api/user/reset_password` | 是 | 修改密码（需校验旧密码） |
| POST | `/api/user/modify_personal_info` | 是 | 修改昵称 / 性别 / 头像 |

### 4.4 Chat（即时聊天，默认 `:8002`）

所有业务接口都需要 `Authorization: Bearer <token>`。会话标识同时支持
`conversationId`（推荐）与历史字段 `groupId`。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/healthz` `/readyz` | 存活 / 就绪探针 |
| POST | `/api/group/message_group_info_list` | 会话列表（含最后一条消息、未读数） |
| POST | `/api/group/mark_read` | 上报已读位点 |
| POST | `/api/message/upload` | HTTP 发送消息 |
| POST | `/api/message/pull` | 按 seq 范围拉取消息（离线补齐 / 历史翻页） |
| POST | `/api/message/sync` | 单会话增量补齐（等价于 `pull(ascending=true)`，供重连补洞） |
| POST | `/api/message/recall` | 撤回消息 |
| POST | `/api/group/add_friend` | 发起好友申请 |
| POST | `/api/group/handle_friend` | 同意 / 拒绝好友申请（`requestId` + `isAgree`） |
| POST | `/api/friend/list` | 好友列表 |
| POST | `/api/friend/request_list` | 好友申请列表（`status` 可选） |
| POST | `/api/group/create_group_chat` | 创建群聊 |
| POST | `/api/group/add_group_chat` | 批量拉人入群 |
| POST | `/api/group/group_user_list` | 会话内的用户 ID 列表 |
| POST | `/api/group/member_list` | 会话成员详情（含昵称、头像、角色） |
| POST | `/api/group/quit` | 退出群聊 |
| GET | `/ws` | WebSocket 长连接 |

#### WebSocket 协议

握手时通过查询参数传令牌（浏览器 WebSocket API 无法自定义请求头）：

```
ws://<host>:8002/ws?token=<accessToken>
```

**上行消息**

```jsonc
{ "type": "ping" }                                    // 心跳，服务端回 {"type":"pong"}

{
  "type": "send",                                     // 发送消息
  "conversationId": "359568819589386240",             // 见下方「大整数注意」
  "content": "hello",
  "msgType": 1,
  "clientMsgId": "客户端生成的幂等键",
  "extra": "{\"k\":\"v\"}"                            // 可选
}
```

**下行消息**

```jsonc
{ "type": "pong", "data": 1789794590900 }

{
  "type": "message",
  "data": {
    "conversationId": "359568819589386240",
    "messageId": "359568832415567873",
    "seq": 6,
    "senderId": "32",
    "type": 1,
    "content": "hello",
    "clientMsgId": "cmid-xxx",
    "createTime": 1789795490355
  }
}

{ "type": "error", "data": "错误描述" }
```

> **大整数注意（重要）**：会话 ID 与消息 ID 是 19 位雪花值，**超出
> JavaScript 的 `Number.MAX_SAFE_INTEGER`（16 位）**。浏览器用 `JSON.parse`
> 读取会被静默舍入（`...784` 变成 `...800`），再把这种被污染的 ID 发回来
> 就会指向一个不存在的会话。
>
> **服务端已做兼容**：`conversationId` 同时接受数字与字符串字面量，
> HTTP 与 WebSocket 两条链路都支持。**客户端应始终把 ID 当作字符串处理。**

#### 心跳与连接参数

| 项 | 值 | 说明 |
| --- | --- | --- |
| 服务端 ping 间隔 | 50s | 主动发 WebSocket Ping 帧 |
| pong 超时 | 60s | 超过则判定连接已死并清理 |
| 上行消息大小上限 | 4096 字节 | 超出会被断开 |
| 单连接发送缓冲 | 256 条 | 缓冲满则主动断开该连接 |
| 单条写超时 | 10s | |
| 在线路由 TTL | 90s | 节点心跳 30s，可容忍 2 次丢失 |

### 4.5 gRPC 接口

协议定义在 `pkg/`，两个服务通过它交互（Chat 依赖 Account）：

| 服务 | 方法 |
| --- | --- |
| `AccountService` | `Register` / `Login` / `VerifyToken` / `GetUser` / `BatchGetUsers` / `ModifyUserInfo` / `ResetPassword` |
| `MessageService` | 消息相关 RPC |
| `ConversationService` | 会话相关 RPC |
| `GroupService` | 群聊相关 RPC |
| `FriendService` | 好友相关 RPC |

---

## 5. 核心流程

### 5.1 发送消息（在线推送）

见 [2.3 消息投递链路](#23-消息投递链路)。

要点：

- 消息的**顺序权威**是会话内 `seq`，由 Redis `INCR` + Lua 脚本原子分配；
- **幂等**由数据库的三重唯一约束强制：`(conversation_id, sender_id, client_msg_id)`
  与 `(conversation_id, sender_id, sender_seq)`；
- 发送者自己也由消费者统一推送（支持多端同步），网关**不再单独回执** ——
  否则发送者会在同一毫秒收到两条指向同一消息、但内容不同的推送。

### 5.2 拉取消息（离线补齐）

```
POST /api/message/pull
{ "conversationId": "...", "fromSeq": 5, "limit": 50, "ascending": true }
```

`fromSeq` 是**排他下界**（`seq > fromSeq`）。客户端记录本地已读的最大 seq，
重连后传该值即可拿到全部缺失消息。

### 5.3 加好友

```
A ──add_friend──▶ 创建 / 复用待处理申请（部分唯一索引保证一对用户只有一条待处理）
                   ▼
B ──handle_friend(isAgree=true)──▶ ① 条件更新申请状态（仅胜出者继续）
                                    ② 双向写入 friend_relation（两行）
                                    ③ 建立单聊会话（幂等）
```

步骤 ① 的条件更新（`WHERE id = ? AND status = 待处理`）保证并发重复提交
同一条申请时只有一个请求能成功，因此不会产生两个会话。

### 5.4 断线重连

客户端重连后：

1. 用 `message_group_info_list` 拿到各会话的 `maxSeq` 与自己的 `lastReadSeq`；
2. 对落后的会话逐个调 `sync`（`fromSeq = lastReadSeq`）补齐，
   `hasMore` 为 true 时以返回的 `maxSeq` 作为新的 `fromSeq` 继续拉；
3. 之后恢复 WebSocket 实时推送。

**离线消息不依赖 MQ 重试**：推送失败（用户不在线）不算错误，重试没有意义；
可靠性由「落库 + 客户端按 seq 拉取」保证。

---

## 6. 数据模型

### 6.1 Account · `im_account`

| 表 | 说明 | 关键约束 |
| --- | --- | --- |
| `account_user` | 用户账号 | PK `id`（雪花）、UNIQUE `email` |

> 表名用 `account_user` 而非 `user`：`user` 是 PostgreSQL 保留字，
> 直接使用会在每次查询时都需要加引号，容易遗漏并引发语法错误。

### 6.2 Chat · `im_chat`

7 张表，见 [0001_init.up.sql](file:///d:/GoCode/IMmessage/im/Chat/migrations/0001_init.up.sql)。

| 表 | 说明 | 关键约束与索引 |
| --- | --- | --- |
| `conversation` | 会话（单聊/群聊统一） | PK `id`；UNIQUE `(type, biz_key)`；`max_seq` 高水位；`member_count` 冗余计数 |
| `conversation_member` | 会话成员 | UNIQUE `(conversation_id, user_id)`；`unread_count` 独立维护；`left_at` 软删除 |
| `message` | 消息 | 三重唯一约束（见下）；`idx_message_conv_seq_desc` 覆盖翻页 |
| `conversation_seq` | 会话序号分片计数器 | PK `(conversation_id, shard)`；消除单行热点 |
| `friend_relation` | 好友关系（双向两行） | UNIQUE `(user_id, friend_id)`；CHECK `user_id <> friend_id` |
| `friend_request` | 好友申请 | 部分唯一索引 `(from_uid, to_uid) WHERE status = 0` |
| `message_outbox` | 本地消息表 | UNIQUE `event_id`；部分索引只捞 `status = 0` |

`message` 表的三重唯一约束：

```sql
UNIQUE (conversation_id, seq)                  -- 顺序权威
UNIQUE (conversation_id, sender_id, client_msg_id)  -- 幂等权威
UNIQUE (conversation_id, sender_id, sender_seq)     -- 防止换 ID 重发绕过
```

**为什么单聊唯一性用 `biz_key` 表达式而不是靠应用层**：单聊键是
`'S:' + LEAST(uidA,uidB) + ':' + GREATEST(uidA,uidB)`，把「谁发起」这一
维度消掉，因此无论 A 还是 B 发起都得到同一个键，数据库层直接拒绝重复会话。

### 6.3 Redis Key

| Key | 类型 | 用途 | TTL |
| --- | --- | --- | --- |
| `im:seq:c:{conversationId}` | String | 会话内序号计数器 | 无 |
| `im:seq:s:{conversationId}:{senderId}` | String | 发送者维度序号 | 无 |
| `im:route:{userId}` | Hash | 在线路由：`field=nodeId, value=心跳毫秒时间戳` | 180s |

> 在线路由用「节点 + 心跳时间戳」而非「节点列表」：多节点部署时同一用户
> 可能被不同节点持有（重连漂移），时间戳可以判断节点是否仍存活，
> 超时节点在查询时被惰性清理，避免消息投递到已宕机的节点。

### 6.4 Kafka Topic

| Topic | Key | 说明 |
| --- | --- | --- |
| `im.message` | 会话 ID | 消息投递事件。key 取会话 ID 保证同一会话进入同一分区，从而保序 |

Topic 名 = `${MQ_TOPIC_PREFIX}.${MQ_TOPIC_MESSAGE}`，便于多环境共用集群时隔离。

---

## 7. 关键设计决策

### 7.1 为什么用「会话内 seq」而不是全局自增主键排序

全局自增主键在多实例写入下无法保证「同一会话内连续」，而客户端需要
连续的序号来判断是否有消息缺失。会话内 seq 让客户端只需比较一个数字
就能知道「我漏了哪几条」。

### 7.2 为什么未读数是独立计数而不是 `max_seq - last_read_seq`

差值推导在以下场景都不准确：退群后重新入群、消息撤回、消息删除。
而且会让高频展示的「未读数」依赖两处数据的实时一致。独立计数把
复杂性收敛到写入路径。

### 7.3 为什么消费者组按节点隔离

每个网关实例都需要收到**全量**消息，才能把消息推给**自己节点上**的在线用户。
若共用消费者组，消息会被实例瓜分，表现为「部分用户永远收不到推送」。

### 7.4 为什么好友关系双向存两行

「我的好友列表」是高频读，`user_id = ?` 单条件索引扫描最快；
若只存一行，查询条件会变成 `user_id = ? OR friend_id = ?`，无法有效利用索引。
代价是写放大 2 倍，但好友关系是低频写（一辈子几次），取舍明显偏向读。

### 7.5 为什么配置全部走环境变量

`*_TYPE` 决定具体实现（如 `DB_TYPE: postgres | polardb-pg | rds-pg`、
`MQ_TYPE: kafka | log`），迁移到云端只需改环境变量，业务代码零改动。
生产环境还会**拒绝**弱 JWT 密钥、空数据库密码与 `log` 类型的 MQ —— 
宁可启动失败，也不带着错误配置对外服务。

### 7.6 为什么不用 `go.work`

三个 module 各自独立（都有自己的 `go.mod`），因此可以被独立引用。
跨项目引用时先推送仓库，再用 `go get` 取伪版本号写入 `go.mod` ——
这与生产环境的依赖解析方式一致，避免「本地能跑、CI 报错」。

---

## 8. 配置说明

全量模板见 [.env.example](file:///d:/GoCode/IMmessage/im/.env.example)。

服务启动时会在**当前工作目录**查找 `.env` 文件（已存在的真实环境变量优先，
不会被覆盖），因此本地开发可以在 `Account/` 与 `Chat/` 下各放一个 `.env`。

| 变量 | 说明 | 本地默认 |
| --- | --- | --- |
| `Environment` | `dev` \| `test` \| `prod` | `dev` |
| `ServiceName` | `account` \| `chat` | — |
| `DB_TYPE` | `postgres` \| `polardb-pg` \| `rds-pg` | `postgres` |
| `DB_HOST` / `DB_PORT` | 数据库地址 | `127.0.0.1` / `5432` |
| `DB_USER` / `DB_PASSWORD` | 数据库账号 | `postgres` / — |
| `DB_NAME` | `im_account` \| `im_chat` | — |
| `DB_SSL_MODE` | `disable` \| `require` \| `verify-full` | `disable` |
| `DB_DSN` | 完整 DSN，设置后优先级最高 | 空 |
| `DB_MAX_OPEN_CONNS` / `DB_MAX_IDLE_CONNS` | 连接池 | `100` / `20` |
| `DB_DEBUG_SQL` | 打印每条 SQL（仅排障用） | 关闭 |
| `CACHE_TYPE` | `redis-standalone` \| `redis-cluster` \| `redis-sentinel` \| `tair` | `redis-standalone` |
| `CACHE_ADDRS` / `CACHE_PASSWORD` | Redis 地址与密码 | `127.0.0.1:6379` / — |
| `MQ_TYPE` | `kafka` \| `log` | `kafka` |
| `MQ_BROKERS` | Kafka 地址列表 | `127.0.0.1:19092` |
| `MQ_TOPIC_PREFIX` / `MQ_TOPIC_MESSAGE` | Topic 名组成 | `im` / `message` |
| `MQ_USERNAME` / `MQ_PASSWORD` | SASL 认证（云 Kafka 用） | 空 |
| `REGISTRY_TYPE` | `etcd` \| `nacos` \| `none` | `etcd` |
| `TRACE_ENABLED` / `TRACE_ENDPOINT` | 链路追踪 | `false` / — |
| `LOG_LEVEL` / `LOG_FORMAT` | 日志级别与格式（`json` \| `text`） | `info` / `text` |
| `HTTP_ADDR` / `GRPC_ADDR` | 监听地址 | `:8001/:9001`（account）、`:8002/:9002`（chat） |
| `WS_ADDR` / `WS_PATH` | WebSocket 对外地址与路径（仅 Chat） | `:8002` / `/ws` |
| `JWT_SECRET` / `JWT_ACCESS_EXPIRE` | 令牌密钥与有效期 | — / `86400` |
| `ACCOUNT_RPC_ENDPOINT` | 账号中心 gRPC 地址（仅 Chat） | `127.0.0.1:9001` |

> `MQ_TYPE=log` 是本地开发与自动化测试模式：它不依赖任何消息中间件，
> 由投递方直接分发给消费者，使「发送 → 落库 → Outbox → 投递 → 消费 → 推送」
> 全链路可以在无 Kafka 的情况下跑通并被断言。**生产环境禁止使用**。

---

## 9. 构建与运行

### 9.1 前置准备

```bash
# 1. 准备环境变量
cp .env.example Account/.env     # 按需修改 DB_NAME=im_account、端口等
cp .env.example Chat/.env        # 按需修改 DB_NAME=im_chat

# 2. 启动本地中间件（PostgreSQL / Redis / Kafka / etcd / Jaeger）
make dev-up

# 3. 建表（两个库分别执行）
psql -h 127.0.0.1 -U postgres -d im_account -f Account/migrations/0001_init.up.sql
psql -h 127.0.0.1 -U postgres -d im_chat    -f Chat/migrations/0001_init.up.sql
```

> 若使用本机原生 PostgreSQL（而非容器实例），可用 `make pg-start` /
> `make pg-stop` / `make pg-status`。

### 9.2 常用命令

```bash
make help          # 查看全部命令
make tidy          # 整理三个 module 的依赖
make proto         # 由 .proto 生成 pb.go
make wire          # 重新生成 Wire 依赖注入代码
make fmt / lint    # 格式化 / 静态检查
make test          # 运行全部测试
make build         # 构建到 bin/account 与 bin/chat
make run-account   # 本地运行账号中心
make run-chat      # 本地运行聊天服务
```

### 9.3 验证服务可用

```bash
curl http://127.0.0.1:8001/healthz
curl http://127.0.0.1:8001/readyz    # 真实探测 DB / Redis，未就绪返回 503
curl http://127.0.0.1:8002/healthz
curl http://127.0.0.1:8002/readyz
```

---

## 10. 部署到云服务器

建议在云上创建**四个应用实例**（也可合并为两个 + 环境隔离）：

| 应用 | `ServiceName` | `Environment` | 说明 |
| --- | --- | --- | --- |
| account-prod | `account` | `prod` | 账号中心正式环境 |
| account-test | `account` | `test` | 账号中心测试环境 |
| chat-prod | `chat` | `prod` | 聊天服务正式环境 |
| chat-test | `chat` | `test` | 聊天服务测试环境 |

关键点：

1. **同一份二进制**，仅通过 `Environment` 与基础设施相关的环境变量区分。
2. 生产环境的 `JWT_SECRET` 必须是**长度 ≥ 32 的强随机串**，否则服务拒绝启动。
3. 生产环境 `MQ_TYPE` 必须是 `kafka`，数据库密码不能为空。
4. 多实例部署时，`TRACE_ENABLED`、服务注册建议开启，并把 `REGISTRY_TYPE`
   从 `none` 切到 `etcd`/`nacos`。
5. `MQ_TOPIC_PREFIX` 建议按环境区分（如 `im-prod` / `im-test`），
   避免多环境共用 Kafka 集群时消息串流。

### 依赖 pkg 的方式

`Account` 与 `Chat` 通过 `go.mod` 引用 `pkg` 的伪版本：

```
require github.com/Jesse-467/im/pkg v0.0.0-<timestamp>-<commit>
```

更新协议后：

```bash
cd pkg && git add -A && git commit -m "..." && git push
cd ../Chat && go get github.com/Jesse-467/im/pkg@<commit> && go mod tidy
```

---

## 11. 测试

```bash
make test                       # 全部 module
cd Chat && go test -race ./...  # 聊天服务（含竞态检测）
```

当前覆盖：

| 包 | 覆盖内容 |
| --- | --- |
| `Chat/internal/xid` | 唯一性、趋势递增、并发不重复、时钟回拨检出、序列用尽自旋 |
| `Chat/internal/biz` | 单聊会话键生成与解析、会话 ID 解析兼容性 |
| `Chat/internal/ws` | 多端连接管理、并发注册投递、连接缓冲、上行消息解码兼容 |
| `Chat/internal/relay` | 退避策略：指数增长、单调性、上限、溢出保护 |
| `Chat/internal/errs` | 错误码映射、cause 链、内部信息不外泄、gRPC 码一致 |
| `Chat/internal/conf` | 必需字段、环境取值、生产环境弱密钥拒绝 |

除单元测试外，仓库还验证过以下端到端场景（A 经 WebSocket 发送 →
B 在线收到推送）：账号注册登录、加好友建会话、鉴权握手、消息实时推送、
多端同步、连发多条 seq 连续无重复、断线后按 seq 拉取补齐。

---

## 12. 容量与扩展性

### 12.1 单节点估算

| 维度 | 估算 | 主要约束 |
| --- | --- | --- |
| WebSocket 并发连接 | 1 万 ~ 3 万 | 每连接 2 个 goroutine + 256 条消息缓冲 |
| 消息发送 QPS | 数千 / 秒 | 每条约 1 次多表事务 + 1 次 Kafka 生产；数据库写入是硬瓶颈 |
| 推送延迟 | 毫秒级 | 落库 → Outbox 轮询（空闲 300ms / 积压 20ms）→ Kafka → 消费推送 |

### 12.2 已具备的扩展能力

- **无状态应用层**：除 WebSocket 连接外无本地状态，可随意多副本。
- **跨节点推送**：消费者组按节点隔离，每个实例独立消费全量消息并推给
  本节点在线用户，无需实例间转发。
- **基础设施可切换**：数据库、缓存、MQ 全部由 `*_TYPE` 环境变量驱动。
- **存储水平扩展**：会话内 seq 由 Redis 分配而非数据库自增，
  数据库分表后序号机制不受影响。

### 12.3 扩展路径

| 目标 | 需要做的事 |
| --- | --- |
| 10 万级在线 | 增加网关实例；Redis 升级 Cluster；Kafka 提升分区数 |
| 消息表亿级 | `message` 按 `conversation_id` 哈希分表 |
| 数据库读写分离 | 接入云 RDS 只读实例；`DB_TYPE` 切换即可 |
| 完整可观测 | 开启 `TRACE_ENABLED`，接入 OTLP 端点 |

---

## 13. 已知限制

| # | 限制 | 影响 | 说明 |
| --- | --- | --- | --- |
| 1 | `conversation_seq` 分片计数器表已建但未启用 | 无 | 当前序号由 Redis 统一分配，该表是为「不依赖 Redis 发号」预留的方案 |
| 2 | 群聊成员列表一次全量加载 | 超大群（万人级）内存占用高 | 消费者已按 500 分批推送，但 `ListMembers` 仍是一次查询 |
| 3 | 撤回未内置时间限制 | 产品策略 | 刻意不做：不同业务线的撤回时限不同，应由调用方决定 |
| 4 | 未实现消息编辑与删除 | 功能缺失 | 数据模型已预留 `status` 字段（`1` 正常 / `2` 撤回 / `3` 删除） |
| 5 | Kafka 消费者处理失败仍提交 offset | 该条推送丢失 | 刻意取舍：失败多因用户离线，重试无意义；可靠性由客户端按 seq 拉取兜底 |
| 6 | WebSocket `CheckOrigin` 允许所有来源 | 依赖 token 鉴权 | 移动端与多域名场景下 Origin 校验会误伤，需要白名单时再开启 |
| 7 | 未接入服务注册（默认 `REGISTRY_TYPE=none`） | 需静态配置地址 | 框架已具备能力，生产环境建议切换为 etcd/nacos |

---

## 附：文档维护约定

- 本文档是项目唯一的总体说明入口。
- **变更同步**：修改 `.proto` / `migrations/*.sql` / `.env.example` /
  `docker-compose.local.yaml` 时，需同步更新本文档的「接口文档」
  「数据模型」「配置说明」对应章节。
- **新增路由**：需更新 [4.4 Chat 接口表](#44-chat即时聊天默认-8002)。
- **修复第 13 节限制后**：请将对应条目移出并说明变更。
- 重构历史与决策记录见 [REFACTOR_PLAN.md](file:///d:/GoCode/IMmessage/im/REFACTOR_PLAN.md)。
