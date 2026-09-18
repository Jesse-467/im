# IM Message —— 基于 go-zero 的分布式即时通讯系统

一个使用 **go-zero** 微服务框架实现的即时通讯（IM）后端系统，提供用户、群组（好友/群聊）、消息收发（WebSocket 实时 + HTTP 离线拉取）三大能力。

---

## 目录

- [1. 技术栈](#1-技术栈)
- [2. 系统架构](#2-系统架构)
- [3. 目录结构](#3-目录结构)
- [4. 服务清单](#4-服务清单)
- [5. 接口文档](#5-接口文档)
- [6. 核心业务流程](#6-核心业务流程)
- [7. 数据模型](#7-数据模型)
- [8. 中间件使用情况](#8-中间件使用情况)
- [9. 配置说明](#9-配置说明)
- [10. 构建与部署](#10-构建与部署)
- [11. 容量与性能评估](#11-容量与性能评估)
- [12. 高可用与扩展性评估](#12-高可用与扩展性评估)
- [13. 已知问题与改进建议](#13-已知问题与改进建议)
- [14. 文档完善度评估](#14-文档完善度评估)

---

## 1. 技术栈

| 分类 | 技术 | 版本 | 用途 |
| --- | --- | --- | --- |
| 语言 | Go | 1.23.1 | 全部服务 |
| 微服务框架 | go-zero | v1.8.3 | REST API + zRPC + 缓存/日志/并发工具 |
| 代码生成 | goctl | 1.8.2 | 由 `.api` / `.proto` 生成骨架 |
| RPC | gRPC + Protocol Buffers | grpc 1.72.0 / protobuf 1.36.6 | 服务间调用 |
| 实时通信 | gorilla/websocket | v1.5.3 | 客户端长连接推送 |
| 关系型数据库 | MySQL | 8.0.26 | 用户、群组、消息持久化 |
| 缓存 / 分布式协调 | Redis | 6.2.5 | go-zero 缓存层、分布式锁、在线状态、群组广播 |
| 消息队列 | Kafka | wurstmeister/kafka | 消息异步投递与削峰 |
| 注册中心（依赖已引入，未启用） | etcd | v3.5.15 | go-zero 依赖，当前未配置使用 |
| 认证 | golang-jwt/jwt | v3.2.2 | JWT 签发与校验 |
| 密码 | golang.org/x/crypto/bcrypt | v0.38.0 | 密码哈希 |
| 参数校验 | go-playground/validator | v10.26.0 | 请求参数校验 + 中文错误翻译 |
| 对象拷贝 | jinzhu/copier | v0.4.0 | API 层与 RPC 层 DTO 互转 |
| 链路追踪 | OpenTelemetry + Jaeger | otel 1.34.0 | 全链路 Trace |
| 日志采集 | Filebeat + Elasticsearch + Kibana | 7.13.4 | 容器日志收集与可视化 |
| 容器化 | Docker / docker-compose | — | 一键部署 |

---

## 2. 系统架构

### 2.1 总体分层

```
                          ┌──────────────────────────────┐
                          │          客户端               │
                          │  HTTP  /  WebSocket           │
                          └───────┬──────────────┬────────┘
                                  │              │
        ┌─────────────────────────┴───┐      ┌───┴──────────────────────┐
        │        API 层 (BFF)          │      │   WebSocket 长连接         │
        │  user-api  :10001            │      │   msg-api  GET /ws        │
        │  msg-api   :10002            │      │   (写回/心跳/广播)         │
        │  group-api :10003            │      └───┬──────────────────────┘
        └───────────┬──────────────────┘          │
                    │ gRPC (直连 Endpoints)        │
        ┌───────────┴──────────────────┐          │
        │        RPC 层 (业务逻辑)      │          │
        │  user-rpc  :20001            │          │
        │  msg-rpc   :20002            │          │
        │  group-rpc :20003            │          │
        └───────┬──────────┬───────────┘          │
                │          │                      │
        ┌───────┴──┐  ┌────┴─────┐   ┌────────────┴─────────────┐
        │  MySQL   │  │  Redis   │   │  Kafka (topic: msg_chat) │
        │ im_message│ │缓存/锁/广播│  │  kafka0:9093 kafka1:9094 │
        └──────────┘  └────┬─────┘   └────────────┬─────────────┘
                           │                      │ 消费
                           │  Pub/Sub 跨节点广播   │
                           └──────────────────────┘
                                    │
                          ┌─────────┴──────────┐
                          │  Jaeger / ELK 观测  │
                          └────────────────────┘
```

### 2.2 关键设计要点

- **API / RPC 分层**：API 层只做鉴权、参数校验、DTO 转换；业务逻辑全部在 RPC 层，便于复用与独立扩缩容。
- **消息写读分离**：上行消息经 `msg-rpc` 落库 MySQL 后投递 Kafka；`msg-api` 消费 Kafka 后通过 WebSocket 推送给在线客户端。
- **离线消息**：客户端通过 HTTP `pull` 接口按 `(uid, platform, groupId)` 维度记录游标 `last_msg_id` 增量拉取。
- **分布式群组管理**：`common/redismgr` 用 Redis + 本地缓存 + Pub/Sub 实现跨节点消息广播，支持多实例部署。
- **单聊 / 群聊统一建模**：单聊 `groupId = min(uid1,uid2)_max(uid1,uid2)`，群聊 `groupId = UUID`，会话表与消息表统一。
- **系统账号**：`uid=1` 微信团队、`uid=2` 文件传输助手，注册时自动建群并写入欢迎消息。

---

## 3. 目录结构

```
im/
├── app/                                # 业务服务（每服务独立的 api / rpc 双端）
│   ├── user/
│   │   ├── api/                        # 用户 HTTP 服务
│   │   │   ├── etc/user-api.yaml       # 配置
│   │   │   ├── internal/{config,handler,logic,svc,types}
│   │   │   ├── user.api                # goctl API 定义
│   │   │   └── user.go                 # 服务入口
│   │   ├── model/                      # 数据模型（goctl model + 自定义方法）
│   │   │   ├── usermodel.go / usermodel_gen.go
│   │   │   ├── user.sql
│   │   │   └── vars.go
│   │   └── rpc/                        # 用户 gRPC 服务
│   │       ├── etc/user-rpc.yaml
│   │       ├── internal/{config,logic,server,svc}
│   │       ├── userclient/             # 生成的客户端
│   │       └── user.go
│   ├── group/                          # 群组/好友服务（结构同上）
│   └── msg/                            # 消息服务（结构同上，含 WS 逻辑）
├── common/                             # 公共组件
│   ├── biz/                            # 业务工具：groupId 拼接/解析、UUID、随机串
│   ├── ctxdata/                        # 从 context 取 uid
│   ├── interceptor/                    # gRPC 统一错误拦截器
│   ├── redismgr/                       # Redis 分布式群组管理器（核心）
│   ├── utils/                          # 通用工具
│   ├── xcrypt/                         # bcrypt 密码哈希 + AES 加解密
│   ├── xerr/                           # 错误码与自定义错误类型
│   ├── xjwt/                           # JWT 签发
│   ├── xlock/                          # 基于 Lua 的 Redis 分布式锁
│   ├── xlog/                           # 日志
│   ├── xmq/                            # Kafka 生产者封装
│   └── xresp/                          # 统一 HTTP 响应 + validator 中文翻译
├── proto/                              # protobuf 定义与生成代码
│   ├── user/  ├── group/  ├── msg/
├── docs/                               # 项目文档
│   ├── Go_knowledge.md                 # Go 知识笔记
│   ├── im_system_interview_qa.md       # 面试 Q&A
│   └── redis-group-manager.md          # Redis 群组管理器设计文档
├── vendor/                             # 依赖（vendor 模式）
├── Dockerfile                          # 六服务单镜像构建
├── docker-compose.yml                  # 中间件 + 应用编排
├── filebeat.yml                        # 日志采集配置
├── init.sql                            # 建表脚本
├── start.sh / start.docker.sh          # 本地 / 容器启动脚本
├── .env                                # 环境变量（已被 .gitignore 忽略）
└── go.mod / go.sum
```

---

## 4. 服务清单

| 服务 | 端口 | 协议 | 说明 |
| --- | --- | --- | --- |
| user-api | 10001 | HTTP/REST | 注册、登录、个人信息、改密 |
| user-rpc | 20001 | gRPC | 用户业务逻辑、在线状态 |
| msg-api | 10002 | HTTP + WS | 消息上传/拉取、WebSocket 长连接、Kafka 消费与广播 |
| msg-rpc | 20002 | gRPC | 消息落库、游标拉取、Kafka 生产 |
| group-api | 10003 | HTTP/REST | 好友、群聊相关接口 |
| group-rpc | 20003 | gRPC | 群组/好友业务逻辑、Kafka 生产 |
| MySQL | 3306 | TCP | 库名 `im_message` |
| Redis | 6379 | TCP | 缓存 / 锁 / PubSub |
| Kafka | 9093、9094 | TCP | 双 Broker，topic `msg_chat` |
| Elasticsearch / Kibana | 9200 / 5601 | HTTP | 日志检索 |
| Jaeger | 16686 | HTTP | 链路追踪 UI |
| Kafka Map | 8080 | HTTP | Kafka 管理后台（admin/admin） |

---

## 5. 接口文档

### 5.1 HTTP 接口

统一响应结构（由 [xresp.go](file:///d:/GoCode/IMmessage/im/common/xresp/xresp.go) 定义）：

```json
{ "code": 0, "msg": "OK", "data": {} }
```

错误码见 [codes.go](file:///d:/GoCode/IMmessage/im/common/xerr/codes.go)：`0` 成功、`400x` 客户端错误、`500x` 服务端/中间件错误。

#### 用户服务（user-api :10001）

| 方法 | 路径 | 鉴权 | 说明 |
| --- | --- | --- | --- |
| POST | `/api/user/register` | 否 | 注册（email / password / nickName / gender） |
| POST | `/api/user/login` | 否 | 登录，返回 `accessToken` + `accessExpire` |
| POST | `/api/user/personal_info` | 是 | 查询本人信息 |
| POST | `/api/user/query_user_info` | 是 | 按 userId 查询他人信息 |
| POST | `/api/user/reset_password` | 是 | 修改密码（旧密码校验） |
| POST | `/api/user/modify_personal_info` | 是 | 修改昵称/性别/头像 |

#### 群组服务（group-api :10003）

| 方法 | 路径 | 鉴权 | 说明 |
| --- | --- | --- | --- |
| POST | `/api/group/add_friend` | 是 | 发起好友申请（创建 status=未通过 的单聊群） |
| POST | `/api/group/handle_friend` | 是 | 同意/拒绝好友申请 |
| POST | `/api/group/group_user_list` | 是 | 查询群成员 userId 列表 |
| POST | `/api/group/message_group_info_list` | 是 | 消息页会话列表（含最后一条消息） |
| POST | `/api/group/create_group_chat` | 是 | 创建群聊 |
| POST | `/api/group/add_group_chat` | 是 | 批量拉人入群（仅限好友） |

#### 消息服务（msg-api :10002）

| 方法 | 路径 | 鉴权 | 说明 |
| --- | --- | --- | --- |
| POST | `/api/message/upload` | 是 | 发送消息（groupId / type / content / uuid） |
| POST | `/api/message/pull` | 是 | 拉取离线消息（platform / groupId / maxMsgId） |
| GET | `/ws` | 是 | WebSocket 长连接，Header 需带 `platform`，URL 需带 JWT |

WebSocket 行为：
- 进入时自动加入用户所属全部群组 + 系统通知群。
- 服务端每 `54s` 发送 ping，`60s` 未收到 pong 判定超时断开。
- 单条消息上限 `1024` 字节，待发缓冲 `15` 条。
- 上行 WS 消息不做业务处理（发送统一走 HTTP upload）。

### 5.2 RPC 接口

定义见 [user.proto](file:///d:/GoCode/IMmessage/im/proto/user/user.proto)、[group.proto](file:///d:/GoCode/IMmessage/im/proto/group/group.proto)、[msg.proto](file:///d:/GoCode/IMmessage/im/proto/msg/msg.proto)。

| 服务 | 方法 |
| --- | --- |
| `UserClient` | `Login` / `Register` / `PersonalInfo` / `ResetPassword` / `UpdateOnlineStatus` / `GetOnlineStatus` / `ModifyUserInfo` |
| `GroupClient` | `AddFriend` / `HandleFriend` / `GroupUserList` / `UserGroupList` / `MessageGroupInfoList` / `AddGroupChat` / `CreateGroupChat` / `GetFriendListByUserId` |
| `MessageClient` | `Upload` / `Pull` |

> 服务端统一使用 `LoggerInterceptor` 将业务错误转换为 gRPC status code。

---

## 6. 核心业务流程

### 6.1 发送消息（在线推送）

```
客户端 ──POST /api/message/upload──▶ msg-api ──gRPC Upload──▶ msg-rpc
                                                                 │
                                              ① MySQL 事务写入 chat_msg（uuid 唯一去重）
                                              ② 投递 Kafka topic=msg_chat
                                                                 │
                                 ◀───────────────────────────────┘
msg-api 的 ConsumeMsgFromMQ 协程消费 Kafka
        │
        ├─▶ RedisGroupManager.BroadcastToGroup(groupId, msg)
        │        ├─ 本节点：写入 group.BroadcastChan → 各 client.onSend
        │        └─ 跨节点：Redis PUBLISH group_broadcast:{groupId}
        │
        └─▶ client.writePump 通过 WebSocket 推送给对应群内所有在线连接
```

### 6.2 拉取离线消息

```
客户端 ──POST /api/message/pull──▶ msg-api ──gRPC Pull──▶ msg-rpc
                                                             │
                    Redis GET "{uid}:{platform}:{groupId}" → lastMsgId
                                                             │
        若 lastMsgId >= maxMsgId 直接返回空（无新消息）
        否则 MySQL 查询 id ∈ (lastMsgId, maxMsgId) 倒序 limit 10
```

### 6.3 加好友

```
A ──add_friend──▶ group-rpc
        ① Redis 分布式锁 lock:{groupId}（Lua SET NX PX，10s）
        ② 校验是否已是好友
        ③ 期望 group 记录（status=2 未通过，type=1 单聊）
        ④ 投递 Kafka 好友申请消息（type=0）
B ──handle_friend(isAgree)──▶ group-rpc
        ① 校验 status 必须为 2
        ② 事务：group.status=1 + 双向 group_user + 问候消息 chat_msg
```

### 6.4 注册

事务内完成：建用户 → 建系统群（微信团队 / 文件传输助手）→ 建 `group_user` → 各写入一条欢迎消息。密码使用 bcrypt 哈希，昵称缺省时随机生成 8 位。

---

## 7. 数据模型

建表脚本见 [init.sql](file:///d:/GoCode/IMmessage/im/init.sql)。

| 表 | 说明 | 关键约束 |
| --- | --- | --- |
| `user` | 用户 | PK `id`，UNIQUE `email` |
| `group` | 会话（单聊/群聊统一） | PK `id`（varchar），`type` 1单聊/2群聊，`status` 1正常/2未通过/3拉黑，`config` JSON |
| `group_user` | 会话成员 | PK `id`，索引 `user_id`、`group_id`，`alias_name` 备注名 |
| `chat_msg` | 聊天消息 | PK `id`（自增，天然有序），UNIQUE `uuid` 幂等，索引 `group_id`，`content` 最长 2048 |

**Redis Key 设计**

| Key | 类型 | 用途 | TTL |
| --- | --- | --- | --- |
| `lock:group:{groupId}` | String | 群组写操作分布式锁 | 10s |
| `{uid}:{platform}:{groupId}` | String | 离线消息拉取游标 | 无 |
| `user:online:{uid}` | String | 在线状态 | login 时无 TTL / update 时 5min |
| `user:last_online:{uid}` | String | 最后在线时间戳 | 无 |
| `nodes` | Set | 全局在线节点集合 | 无 |
| `group:{groupId}` | Hash | 群组 → 节点映射（field=nodeID, value=时间戳） | 24h |
| `node:{nodeID}` | String | 节点心跳 | 2min |
| `group_broadcast:{groupId}` | Pub/Sub Channel | 跨节点广播 | — |

---

## 8. 中间件使用情况

### 8.1 MySQL ✅ 基本合理

- 使用 go-zero `sqlx` + `cache` 组合，单条查询自动缓存、写操作自动失效，隔离级别由 `TransactCtx` 保证。
- 唯一索引承担幂等职责（`chat_msg.uuid`、`user.email`），设计正确。
- ⚠️ `chat_msg` 是单表无分区的消息大表，长期运行会成为最大瓶颈；`pull` 查询按 `group_id` 过滤后 `id` 排序，建议补 `(group_id, id)` 复合索引。

### 8.2 Redis ✅ 使用充分，但存在缺陷

- 分布式锁采用 Lua 脚本（`SET NX PX` + 值比对删除），实现是**正确**的经典写法（见 [lock.go](file:///d:/GoCode/IMmessage/im/common/xlock/lock.go)）。
- 群组广播：本地缓存 + Redis Pub/Sub，避免全量走 Redis，性能设计合理。
- ⚠️ 缺陷见 [第 13 节](#13-已知问题与改进建议)：`online` key 无 TTL、Pub/Sub 无持久化、`LastActive` 未刷新。

### 8.3 Kafka ⚠️ 可用但有丢消息风险

- 生产者 `segmentio/kafka-go`，`BatchTimeout=20ms`，两个 Broker，消费组 `msg-api`。
- ⚠️ `Writer` 未设置 `RequiredAcks`（默认 `RequireNone`），Broker 未确认即返回成功，存在**静默丢消息**风险。
- ⚠️ `docker-compose` 中 `KAFKA_AUTO_CREATE_TOPICS_ENABLE=false`，但仓库内无创建 topic 的脚本/初始化逻辑，需手工建 topic。
- ⚠️ 消费者 `FetchMessage` 出错即 `break` 退出循环，且广播失败仍提交 offset，缺乏重试与死信机制。

### 8.4 观测组件 ✅ 已接线

- 六个服务均开启 Jaeger 上报（`Telemetry.Batcher: jaeger`）。
- Filebeat → ES → Kibana 采集容器日志，按日期分索引。

---

## 9. 配置说明

各服务配置位于 `app/<svc>/{api,rpc}/etc/*.yaml`，通过 go-zero `conf.UseEnv()` 读取环境变量占位符；`.env` 由 `godotenv` 在 `main` 中加载。

| 环境变量 | 说明 | 示例 |
| --- | --- | --- |
| `TZ` | 时区 | `Asia/Shanghai` |
| `MYSQL_HOST` / `MYSQL_PORT` | MySQL 地址 | `mysql` / `3306` |
| `MYSQL_PASSWORD` | MySQL root 密码 | `123456` |
| `DBNAME` | 数据库名 | `im_message` |
| `REDIS_HOST` / `REDIS_PORT` / `REDIS_PASSWORD` | Redis 连接 | `redis` / `6379` / `123456` |
| `KAFKA0` / `KAFKA1` | Kafka Broker 地址 | `kafka0` / `kafka1` |
| `JAEGER_AGENT_HOST` | Jaeger 地址 | `jaeger` |
| `SERVER_IP` | 本机 IP（user-rpc 遥测用） | `127.0.0.1` |
| `JWT_SECRET` | JWT 签名密钥 | 自定义强随机串 |

> ⚠️ 仓库内**未提供 `.env.example`**，且 `.env` 被 `.gitignore` 忽略。新环境部署必须先手工创建 `.env`，建议补充模板文件。

---

## 10. 构建与部署

### 10.1 本地开发

```bash
# 1. 准备环境变量
cp .env.example .env   # 当前需手工创建 .env，见第 9 节

# 2. 启动中间件
docker compose up -d mysql redis kafka0 kafka1 zookeeper

# 3. 手工创建 Kafka topic（必须）
#    topic: msg_chat，分区数 >= msg-api 副本数

# 4. 启动服务（先 RPC 后 API）
./start.sh
# 或分别启动
go run app/user/rpc/user.go -f app/user/rpc/etc/user-rpc.yaml
go run app/user/api/user.go -f app/user/api/etc/user-api.yaml
# ... group / msg 同理
```

### 10.2 容器部署

```bash
docker build -t im_message:latest .
docker compose up -d
```

镜像内包含全部 6 个二进制，由 `start.docker.sh` 依次拉起。

### 10.3 代码生成

```bash
goctl api go   -api app/user/api/user.api   -dir app/user/api
goctl rpc protoc proto/user/user.proto --go_out=. --go-grpc_out=. --zrpc_out=app/user/rpc
goctl model mysql datasource -url="root:pwd@tcp(127.0.0.1:3306)/im_message" -table="user" -dir app/user/model
```

---

## 11. 容量与性能评估

### 11.1 单节点理论能力（估算）

| 维度 | 估算 | 主要约束 |
| --- | --- | --- |
| WebSocket 并发连接 | 1 万 ~ 3 万 / 节点 | 每连接 2 个 goroutine + 15 条消息缓冲，单机 Go 协程与内存可支撑；受 `maxMessageSize=1024` 与 GC 影响 |
| 消息发送 QPS | 数千 / 秒 | 每条约 1 次 MySQL 写入 + 1 次 Kafka 生产；MySQL 单实例写入是硬瓶颈 |
| 消息推送延迟 | 毫秒级 | DB 写入 → Kafka → 消费 → 广播，链路中含 20ms 批量窗口 |

### 11.2 整体规模结论

> 在当前**默认单实例部署 + 单点 MySQL/Redis** 配置下，建议按 **数千同时在线、数万注册用户** 的规模设计。

若完成以下改造，可平滑扩展到 **10 万级同时在线**：

1. 6 个服务拆分为独立容器/Deployment，按需独立扩缩容（尤其 `msg-api`）。
2. RPC 客户端由硬编码 `127.0.0.1:port` 切换为 **etcd 服务发现**（框架已具备能力）。
3. MySQL 引入主从 + 读写分离，`chat_msg` 按 `group_id` 哈希分表或按时间分区。
4. Redis 升级为 Cluster / Sentinel。
5. Kafka 提升 topic 分区数并设置 `RequiredAcks=RequireAll`，`msg-api` 多副本消费。

---

## 12. 高可用与扩展性评估

### 12.1 高可用（HA）

| 项 | 现状 | 评价 |
| --- | --- | --- |
| 无状态 API/RPC | 逻辑无本地状态（除 WS 连接） | ✅ 可多副本 |
| WebSocket 跨节点 | Redis Pub/Sub + 本地缓存广播 | ✅ 已支持多实例 |
| 节点故障清理 | 心跳 + 5 分钟清理过期节点 | ✅ 有自愈机制 |
| 服务发现 | 使用静态 `Endpoints` 直连 | ❌ 无法自动发现/故障摘除 |
| MySQL | compose 中单实例，无主从 | ❌ 单点 |
| Redis | compose 中单实例，无哨兵/集群 | ❌ 单点 |
| Kafka | 双 Broker 但无副本配置 | ⚠️ 部分冗余 |
| 消费者容错 | 出错 `break` 退出，无重试 | ❌ 消费者可能永久停止 |

**结论：应用层已具备横向扩展的设计基础，但中间件层全部是单点，整体不具备生产级高可用。**

### 12.2 可扩展性

- ✅ 分层清晰（API / RPC / Model / Common），新增业务只需按 `goctl` 规范加 logic。
- ✅ 消息通过 Kafka 解耦，推送侧与写入侧可独立扩容。
- ⚠️ 单镜像打包 6 个服务，只能整体扩缩容，无法按热点服务独立伸缩。
- ❌ RPC 地址硬编码，扩容 RPC 需逐台改配置。
- ❌ 无网关层，客户端需感知 3 个不同端口。

---

## 13. 已知问题与改进建议

按优先级排列。

### P0 — 正确性 / 稳定性

| # | 问题 | 位置 | 建议 |
| --- | --- | --- | --- |
| 1 | Kafka 生产者未设 `RequiredAcks`，Broker 未确认即返回，可能静默丢消息 | [servicecontext.go](file:///d:/GoCode/IMmessage/im/app/msg/rpc/internal/svc/servicecontext.go#L28-L32) | 设置 `RequiredAcks: kafka.RequireAll`，并加 `MaxAttempts` 重试 |
| 2 | 消费者 `FetchMessage` 出错直接 `break`，协程永久退出，此后**不再推送任何消息** | [wslogic.go](file:///d:/GoCode/IMmessage/im/app/msg/api/internal/logic/wslogic.go#L61-L65) | 改为 `continue` + 退避重试，仅在 `context` 取消时退出 |
| 3 | 广播失败仍提交 offset，消息对该节点在线用户永久丢失 | [wslogic.go](file:///d:/GoCode/IMmessage/im/app/msg/api/internal/logic/wslogic.go#L79-L88) | 按错误类型决定是否提交，失败进入重试队列 |
| 4 | `LocalClient.LastActive` 仅在入群时赋值，从不刷新；清理协程 5 分钟后会**把活跃连接踢出群组** | [group_manager.go](file:///d:/GoCode/IMmessage/im/common/redismgr/group_manager.go#L90), [#L332-L338](file:///d:/GoCode/IMmessage/im/common/redismgr/group_manager.go#L332-L338) | 在 `handleLocalBroadcast` / 收到 pong 时刷新 `LastActive`，或改用心跳续期 |
| 5 | `msg-rpc.yaml` 使用 `${MYSQL_HOSTS}`，而 `.env` 定义为 `MYSQL_HOST`，占位符不一致 | [msg-rpc.yaml](file:///d:/GoCode/IMmessage/im/app/msg/rpc/etc/msg-rpc.yaml#L10) | 统一为 `MYSQL_HOST` |
| 6 | `user:online:{uid}` 登录时以 TTL=0 写入且永不过期，用户会**永久显示在线** | [loginlogic.go](file:///d:/GoCode/IMmessage/im/app/user/rpc/internal/logic/loginlogic.go#L63-L65) | 统一设置 TTL 并由心跳续期 |

### P1 — 架构 / 运维

| # | 问题 | 建议 |
| --- | --- | --- |
| 7 | RPC 硬编码 `127.0.0.1` 端点，无法水平扩展 | 接入 etcd 服务发现（go-zero 原生支持） |
| 8 | 6 服务打包进单容器，无法独立扩缩容 | 拆分为独立镜像/Deployment |
| 9 | `Timeout: 10000000`（毫秒 ≈ 2.7 小时）远超合理值，且各服务不统一（5s~2.7h） | 按业务设定，如 API 3~5s、RPC 1~3s |
| 10 | 无 Kafka topic 初始化脚本，`auto.create=false` 会导致启动即失败 | 增加初始化 Job 或 compose 内建 topic |
| 11 | 缺少 `.env.example`，`.env` 被忽略，新环境无法快速启动 | 补充模板文件 |
| 12 | 密码/JWT 密钥明文写在 `.env`，且 `JWT_SECRET` 强度偏弱 | 接入密钥管理，禁用弱密钥 |
| 13 | `docs/redis-group-manager.md` 声称"Pub/Sub 具备持久化机制保证消息不丢失"，与事实不符 | 修正文档，说明 Pub/Sub 为 at-most-once |
| 14 | Dockerfile `EXPOSE 30001 30002` 与实际端口（10003/20003）不符 | 修正为 `10003 20003` |
| 15 | 无单元测试与集成测试 | 补充核心逻辑测试 |

### P2 — 性能 / 代码质量

| # | 问题 | 建议 |
| --- | --- | --- |
| 16 | `chat_msg` 无 `(group_id, id)` 复合索引，拉取按 `id` 排序会有额外排序开销 | 增加复合索引 |
| 17 | `MessageGroupInfoList` 对每个会话串行查 4 次（group/user/group_user/chat_msg），N+1 查询 | 批量查询或 `mr.MapReduce` 并发化 |
| 18 | `group_manager.cleanupExpiredNodes` 使用 `KEYS group:*`，会阻塞 Redis | 改用 `SCAN` 游标遍历 |
| 19 | `readPump` 断开时按 `Resp.List` 全量 `LeaveGroup`，群组多时开销大 | 仅在最后一条连接断开时清理 |
| 20 | `rand.Seed` 已被 Go 1.20+ 弃用，且 `biz.RandStr` 存在并发竞争 | 使用 `math/rand/v2` 或 `crypto/rand` |
| 21 | 日志混用 `logx` 与 `fmt.Println`（如 pulllogic、messagegroupinfolistlogic） | 统一为 `logx` |
| 22 | `PullLogic` 在 `lastMsgId >= maxMsgId` 时返回 `(nil, nil)`，API 层会得到 `data: null` | 返回空结构体更利于客户端处理 |
| 23 | 群成员加入未校验权限（`add_group_chat` 已校验好友，但成员上限、重复加入未处理） | 补业务校验 |

---

## 14. 文档完善度评估

| 项目 | 现状 | 评价 |
| --- | --- | --- |
| README | 本文档 | ✅ 已补全 |
| 接口文档 | `app/*/api/*.api` 与 `proto/*/*.proto` 有完整注释 | ✅ 定义清晰，但无 Swagger/OpenAPI 导出 |
| 配置文档 | 各 yaml 有行内注释 | ⚠️ 缺少统一的环境变量表与 `.env.example` |
| 架构文档 | `docs/redis-group-manager.md` | ⚠️ 与实现有偏差（Key 命名、持久化描述），需同步更新 |
| 部署文档 | `start.sh` / `docker-compose.yml` | ⚠️ 缺少 Kafka topic 初始化说明 |
| 数据字典 | `init.sql` | ⚠️ 字段注释不完整（如 `group.config` 结构未说明） |
| 测试文档 | 无 | ❌ 缺失 |

**建议后续补充**：`Swagger/OpenAPI` 自动导出（goctl 支持 `-api` 生成）、`docs/` 下增加「架构设计」「部署运维」「故障排查」三篇，并在 `docs/redis-group-manager.md` 中修正与实际 Redis Key 不一致之处。

---

## 附：文档维护约定

- 本文档位于仓库根目录 `README.md`，是项目唯一的总体说明入口。
- **变更同步**：修改 `*.api` / `*.proto` / `init.sql` / `docker-compose.yml` / `etc/*.yaml` 时，需同步更新本文档的「接口文档」「数据模型」「配置说明」「服务清单」对应章节。
- **新增服务**：需在「服务清单」「目录结构」「接口文档」中补充相应内容。
- **修复第 13 节问题后**：请将对应条目移出「已知问题」，并在变更说明中标注。
