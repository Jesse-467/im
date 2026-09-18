# IM 即时聊天系统 —— go-zero → Kratos + Gin 重构章程

> 版本：v1.1（评审通过，执行中）　基线分支：`rebuild/kratos-gin`
> 目标仓库：`im_message`（即时聊天微服务）
> 适用范围：本仓库全部代码的推倒重建

---

## 修订记录

### v1.1 —— 结构性调整（**本节优先级高于下文所有冲突内容**）

相较 v1.0，以下 8 点变更已确认并生效：

1. **不备份旧代码**。旧实现、旧表结构、旧数据均不作为参考，完全从零设计（仅功能对齐）。§6 Phase 0 中的备份动作作废。
2. **单 Git 仓库 + 三个独立 Go module**：`Account/`、`Chat/`、`pkg/`，结构见下。
3. **`pkg/` 只放跨服务协议**（`.proto` + 生成的 `.pb.go`），**禁止**任何工具库、常量、第三方依赖。
4. **`Account` 与 `Chat` 之间零代码共享**，只允许通过 `pkg/` 中的 gRPC 契约交互。各自实现自己的配置、日志、中间件、客户端封装。
5. **基础设施全部环境变量驱动**，通过 `*_TYPE` 变量选择实现：本地用 docker 组件，云端可无缝切到阿里云（RDS / Tair / 云 Kafka 或 RocketMQ / Nacos）。
6. **多环境部署模型**：`Account`、`Chat` 各部署 proc/test 两份，共 **4 个应用**，由 `Environment` 变量区分。
7. **部署目标为阿里云 SAE 容器**，每 module 独立镜像，可独立伸缩。
8. 技术选型按 §2.2 定版（Kratos **v2.9.2**、Gin、GORM v2、franz-go、gorilla/websocket、etcd）。

#### v1.1 仓库与模块结构

```
im/                                  # 单一 Git 仓库
├── Account/                         # 独立 Go module —— 账号中心服务
│   ├── go.mod                       # module im/account
│   ├── cmd/account/{main.go,wire.go,wire_gen.go}
│   ├── internal/
│   │   ├── conf/                    # 环境变量解析 + 强校验
│   │   ├── server/                  # httpserver.go(Gin) / grpcserver.go
│   │   ├── service/                 # 薄适配层：DTO ↔ biz
│   │   ├── biz/                     # 领域用例 + Repository 接口
│   │   └── data/                    # Repository 实现 + mysql/redis 客户端
│   ├── migrations/                  # 本服务独立的库表迁移
│   └── Dockerfile
├── Chat/                            # 独立 Go module —— 即时聊天服务（★ 核心）
│   ├── go.mod                       # module im/chat
│   ├── cmd/chat/{main.go,wire.go,wire_gen.go}
│   ├── internal/
│   │   ├── conf/
│   │   ├── server/                  # httpserver(Gin) / wsserver / grpcserver / consumer
│   │   ├── service/                 # conversation / message / group / friend / gateway
│   │   ├── biz/                     # 领域层：纯业务，零框架依赖
│   │   └── data/                    # model / repo / client(mysql,redis,kafka,account-rpc)
│   ├── migrations/
│   └── Dockerfile
├── pkg/                             # 独立 Go module —— 仅跨服务协议
│   ├── go.mod                       # module im/protocol
│   ├── account/v1/                  # account.proto + *.pb.go
│   ├── chat/v1/                     # chat 对外契约 + *.pb.go
│   └── Makefile                     # protoc 生成脚本
├── deploy/                          # 本地编排 / 镜像 / SAE 清单
│   ├── docker-compose.local.yaml    # 本地中间件（mysql,redis,kafka,etcd,jaeger,...）
│   └── init/                        # Kafka topic、DB 初始化脚本
├── go.work                          # 本地开发工作区（仅开发用，不参与镜像构建）
├── Makefile                         # 根级统一入口
├── .env.example                     # 全量环境变量模板
└── REFACTOR_PLAN.md
```

**硬性约束（写入 CI 校验）**

- `pkg/` 内出现任何非 `.proto` / 非生成 `.pb.go` 的 `.go` 文件 → 构建失败。
- `Account` 与 `Chat` 互相 import → 构建失败（跨服务只能走 gRPC）。
- 每个 module 独立 `go.mod`；本地通过 `replace im/protocol => ../pkg` 引用；`go.work` 仅用于本地开发。

#### v1.1 多环境部署模型

| 部署单元 | `Environment` | 说明 |
| --- | --- | --- |
| `account-test` | `test` | 账号中心 · 测试 |
| `account-prod` | `prod` | 账号中心 · 正式 |
| `chat-test` | `test` | 聊天服务 · 测试 |
| `chat-prod` | `prod` | 聊天服务 · 正式 |

`Environment` 决定：日志级别与格式、是否开启 gRPC reflection / pprof、链路采样率、配置校验严格度、是否启用调试路由。

#### v1.1 基础设施环境变量规范（可插拔）

代码只依赖抽象接口，具体实现由 `*_TYPE` 在启动时选择；接入新的云组件只需新增一个实现，业务代码零改动。

```
# ── 运行环境 ──
Environment            = dev | test | prod
ServiceName            = account | chat

# ── 关系型数据库 ──
DB_TYPE                = mysql | polardb | rds
DB_HOST / DB_PORT / DB_USER / DB_PASSWORD / DB_NAME
DB_MAX_OPEN_CONNS / DB_MAX_IDLE_CONNS / DB_CONN_MAX_LIFETIME
DB_DSN                 # 可选：直接指定 DSN，优先级最高

# ── 缓存 ──
CACHE_TYPE             = redis-standalone | redis-cluster | redis-sentinel | tair
CACHE_ADDRS            # 逗号分隔
CACHE_PASSWORD / CACHE_DB
CACHE_POOL_SIZE

# ── 消息队列 ──
MQ_TYPE                = kafka | aliyun-kafka | rocketmq
MQ_BROKERS / MQ_TOPIC_PREFIX / MQ_USERNAME / MQ_PASSWORD
MQ_CONSUMER_GROUP

# ── 注册中心 / 服务发现 ──
REGISTRY_TYPE          = etcd | nacos | none
REGISTRY_ADDRS / REGISTRY_NAMESPACE

# ── 可观测 ──
TRACE_ENABLED / TRACE_ENDPOINT / TRACE_SAMPLER_RATIO
METRICS_ENABLED / METRICS_PATH
LOG_LEVEL / LOG_FORMAT

# ── 应用自身 ──
HTTP_ADDR / GRPC_ADDR
JWT_SECRET
ACCOUNT_RPC_ENDPOINT   # Chat 依赖 Account 的地址
```

> 下文 §2.1 / §2.3 中的应用边界图与目录树、§6 Phase 0 的备份动作、§12 的决策点表，凡与本节冲突者，**一律以本节为准**。

### v1.3 —— 数据库由 MySQL 切换为 PostgreSQL（**优先级最高**）

1. **数据库选型改为 PostgreSQL 17**，`DB_TYPE` 取值 `postgres`（本地）| `polardb-pg` | `rds-pg`。云端产品同样兼容 PG 协议，只改地址与 SSL 要求即可，业务代码零改动。
2. **驱动换为 `gorm.io/driver/postgres`（pgx）**，移除 `go-sql-driver/mysql` 与 `gorm.io/driver/mysql`。DSN 采用 PostgreSQL 的**关键字/值形式**（`host=... port=... user=...`）而非 URL 形式——口令中常含 `@ : /`，URL 形式需要转义。
3. **表名 `user` → `account_user`**：`user` 是 PostgreSQL 保留字（等价于 `current_user`），不加引号会直接报语法错误。
4. **唯一约束冲突的判定改用 SQLSTATE `23505`**（原为 MySQL 的 1062），并直接比对字面量而不引入官方错误码常量包。
5. **时间类型统一为 `TIMESTAMPTZ`**。注意 PostgreSQL 没有 MySQL 的 `ON UPDATE CURRENT_TIMESTAMP`，`updated_at` 改由应用侧（GORM 的 `autoUpdateTime`）维护。
6. **本地编排的 MySQL 容器换成 `postgres:17-alpine`**，映射端口 `15432`（避开宿主机 5432，便于与本机原生实例共存），数据存放在命名卷 `postgres_data`，建库脚本挂在 `deploy/init/postgres/`。
7. **本机同时提供原生 PostgreSQL 17.2（免安装二进制）**，位于 `D:\pgsql`，数据目录 `im/.localdb/pgdata`（已 gitignore）。两者任选其一，`.env.example` 默认指向原生实例的 `5432`。

### v1.2 —— 模块引用方式与编排修正（**优先级高于 v1.1 及下文所有冲突内容**）

1. **不再使用 `go.work`**。工作区会把多个 module 隐式耦合在一起，掩盖真实依赖，也无法验证 module 是否真的可独立构建与发布。`go.work` / `go.work.sum` 已加入 `.gitignore`。
2. **跨 module 引用统一走仓库依赖**，流程固定为：
   `git commit` → `git push` → `go get github.com/Jesse-467/im/pkg@<commit-sha>`（解析为伪版本）→ 写入 `go.mod`。
   代价是每次修改 `pkg` 契约都需要一次推送；收益是依赖关系真实、版本可追溯，且两个应用确实能独立构建。
3. **module 路径与仓库对齐**（仓库为 `github.com/Jesse-467/im`，公开仓库，`go get` 可直接解析）：
   - `github.com/Jesse-467/im/pkg`（目录 `pkg/`）
   - `github.com/Jesse-467/im/Account`（目录 `Account/`）
   - `github.com/Jesse-467/im/Chat`（目录 `Chat/`）
   所有 `proto` 的 `go_package` 已同步为该形式的完整路径。
4. **修正 v1.0 的 Phase 2 定义**：原计划在 `pkg` 内建 `xgin / xkafka / xws` 等基建库，这与 v1.1「pkg 只放协议」冲突。**Phase 2 作废**，相关基建代码分别在 `Account/internal` 与 `Chat/internal` 内各自实现，两边不共享。后续阶段编号顺延。
5. **本地编排改用非默认端口**，避免与开发机上已有的 MySQL / Redis 冲突：
   MySQL `13306`、Redis `16379`、Kafka `19092`、etcd `12379`。`.env.example` 已对齐。
6. **基线分支**：`rebuild/kratos-gin`（已推送至 origin）。

---

## 0. 一句话目标

将当前 **go-zero 单体式多服务**（6 个 goctl 生成的服务：user/group/msg × api/rpc + 一坨 `common/`）**完全推倒重建**，改为 **Kratos v2 + Gin** 的企业级工程结构：明确划分为 **账号中心（account）** 与 **即时聊天（chat）** 两个可独立部署的应用，`chat` 为本次核心交付物，功能对齐现有接口，同时补齐可靠性、顺序性、可观测性与可扩展性。

---

## 1. 现状与问题清单（重构动机）

### 1.1 现状骨架

```
im/
├─ app/{user,group,msg}/{api,rpc}/   # 6 个 goctl 服务，各自 config/handler/logic/svc/types
├─ common/                            # 9 个 x* 散装工具包
├─ proto/                             # 3 个 .proto（与 app 目录松耦合）
├─ vendor/                            # 全量 vendored 依赖
└─ Dockerfile                         # 6 个二进制打进 1 个镜像
```

### 1.2 必须解决的问题（源自 README 第 13 节）

| 分类 | 问题 | 重构后的解法 |
| --- | --- | --- |
| **工程结构** | 6 服务 6 套 config/types/svc，模板代码重复度极高；`common/` 无分层、无单一职责 | Kratos 四层（server/service/biz/data）+ `pkg/` 基建库 |
| **依赖注入** | 全局变量 + 手工 `NewServiceContext`，装配顺序靠人工维护 | **Wire** 编译期 DI，`ProviderSet` 声明式装配 |
| **错误处理** | 自定义 `xerr` + gRPC interceptor 手工转换，API/RPC 两套 | Kratos 统一 `errors` + 错误码映射中间件 |
| **配置** | 每个服务一份 yaml，占位符不一致（`MYSQL_HOSTS` vs `MYSQL_HOST`），无 `.env.example` | 统一 `configs/` 分层 + 校验 + 模板文件 |
| **服务发现** | RPC 端点硬编码 `127.0.0.1:port`，无法扩容 | **etcd** 注册发现（Kratos 原生） |
| **消息可靠性** | Kafka `RequiredAcks` 默认 none；消费失败仍提交 offset；消费者出错即退出 | Outbox 本地消息表 + `acks=all` + 手动提交 + 重试/DLQ |
| **消息顺序性** | 依赖 DB 自增 id 当游标，跨会话/分表后失效 | **会话级 seq**，同会话严格有序 |
| **推送可靠性** | Redis Pub/Sub（at-most-once）广播，丢消息无感知 | **每实例独立 Kafka consumer group**（goim 模式） |
| **在线状态** | `user:online:*` TTL=0 永不过期，用户永久在线 | TTL + 心跳续期 + 多端会话模型 |
| **连接清理** | `LastActive` 从不刷新，活跃连接 5 分钟后被踢出群 | WS 心跳驱动续期 + 连接生命周期管理 |
| **超时** | `Timeout: 10000000`（≈2.7 小时） | 按接口分级设置（100ms ~ 10s） |
| **可观测** | 仅接 Jaeger，无 metrics、无业务指标 | OTel Trace + Prometheus Metrics + 结构化日志 + 看板告警 |
| **安全** | `JWT_SECRET` 弱密钥明文；无接口限流 | 密钥外置 + 按用户/接口限流 + 熔断 |
| **测试** | 零测试 | 单测 + testcontainers 集成测试 + 契约测试 + 压测 |
| **部署** | 6 服务单镜像，无法独立伸缩；Kafka topic 需手工创建且无脚本 | 每应用独立镜像 + 初始化 Job + K8s 清单 |

---

## 2. 目标架构

### 2.1 应用边界（核心决策）

```
┌─────────────────────────────────────────────────────────────────────┐
│                       im_message 仓库（单仓）                        │
│                                                                     │
│   app/account  ──────────gRPC──────────▶  app/chat                  │
│   （账号中心）            BatchGetUser      （即时聊天 · 本次核心）    │
│                          VerifyToken                                │
│   · 注册 / 登录 / 改密      GetUserRelations │  · 私聊 / 群聊会话      │
│   · 用户资料 / 在线状态                     │  · 好友关系             │
│   · 对外 REST: /api/user/*                 │  · 消息收发 / 离线补偿   │
│   · gRPC: account.v1                       │  · WebSocket 长连接     │
│                                            │  · 对外 REST: /api/*    │
│                                            │  · gRPC: chat.v1        │
│            ▲                               │                         │
│            └────────── pkg/ 共享基建 ──────┘                         │
└─────────────────────────────────────────────────────────────────────┘
```

**职责划分原则**

- `account`：**账号的唯一权威来源**。拥有 `user` 表，负责身份、凭证、资料。聊天系统只通过 gRPC 读取它，**不持有任何账号写权限**。
- `chat`：**聊天领域的唯一权威来源**。拥有会话、成员、好友、消息数据。用户昵称/头像**不落本地业务表**（可缓存），通过 `account.BatchGetUser` 实时取用，避免跨服务数据双写。
- 两个应用**共享** `pkg/`，但**不共享** `internal/`，边界靠 protobuf 契约强制。

> **决策点 A**：若你希望本次只交付 `chat`，`account` 仅保留 proto 契约 + 一个 mock server，请在评审时说明。默认按 **两个应用都实现** 执行（保证"功能与现状一致"且可端到端跑通）。

### 2.2 技术栈（定版）

| 分类 | 选型 | 版本 | 说明 |
| --- | --- | --- | --- |
| 语言 | Go | 1.23+ | 与现状一致 |
| 微服务框架 | **go-kratos/kratos/v2** | v2.8.x | 分层 / DI / 配置 / 日志 / 中间件 / 注册发现 |
| HTTP 引擎 | **gin-gonic/gin** | v1.10.x | 通过实现 `transport.Server` 接口接入 Kratos 生命周期 |
| RPC | google.golang.org/grpc | v1.7x | 应用间调用；protobuf 契约 |
| IDL 工具链 | **buf** + protoc-gen-go / -go-grpc | 最新 | 替代 `option go_package="../user"` 这类反模式 |
| 依赖注入 | **google/wire** | v0.6+ | 编译期，无反射 |
| ORM | **gorm.io/gorm** v2 + gorm/gen | v1.25+ | Repository 接口隔离，禁止在 biz 层出现 `*gorm.DB` |
| 迁移 | **golang-migrate** | v4 | 版本化 SQL，替代单个 `init.sql` |
| 缓存 | **redis/go-redis/v9** | v9.8+ | 缓存 / 锁 / 路由 / 限流 |
| 分布式锁 | **go-redsync/redsync** | v4 | 替代手写 Lua（保留可测试性） |
| 消息队列 | **twmb/franz-go** | v1.17+ | 支持 `acks=all`、幂等生产、事务；比 segmentio 更可控 |
| WebSocket | **gorilla/websocket** | v1.5.3 | 成熟稳定，社区最大 |
| 配置 | kratos config（file + env） | — | yaml 分层 + 环境变量覆盖 |
| 日志 | kratos log（zap） | zap 1.27 | JSON 结构化，带 traceId |
| 链路追踪 | OpenTelemetry + Jaeger | otel 1.3x | 全链路 |
| 指标 | Prometheus + Grafana | — | 业务 + 中间件指标 |
| 注册中心 | **etcd** | v3.5 | Kratos 原生 |
| 限流/熔断 | kratos ratelimit / circuitbreaker | — | 中间件 |
| 测试 | testify + testcontainers-go + k6 | — | 单测 / 集成 / 压测 |
| 容器 | Docker + docker-compose | — | 本地基建；K8s 清单预留 |

> 依赖不再 `vendor/`，改用 Go Module Proxy（`GOPROXY`），镜像构建走多阶段 + `go mod download` 缓存。

### 2.3 目标目录结构

```
im/
├─ api/                                     # 契约层：只放 .proto 与生成物
│   ├─ account/v1/account.proto             # 账号中心对外契约（chat 依赖它）
│   ├─ chat/v1/{conversation,message,group,friend,gateway}.proto
│   └─ buf.yaml / buf.gen.yaml
│
├─ app/
│   ├─ account/                             # 账号中心（独立部署单元）
│   │   ├─ cmd/account/{main.go,wire.go,wire_gen.go}
│   │   ├─ configs/{config.yaml,config.dev.yaml}
│   │   └─ internal/
│   │       ├─ conf/                        # 配置结构体 + 校验
│   │       ├─ server/                      # httpserver.go(gin) / grpcserver.go
│   │       ├─ service/                     # 薄适配：DTO ↔ biz（无业务逻辑）
│   │       ├─ biz/                         # 领域用例 + 实体 + Repository 接口
│   │       │   └─ user/
│   │       └─ data/                        # Repository 实现 + 外部依赖
│   │           └─ user/
│   │
│   └─ chat/                                # 即时聊天（★ 本次核心）
│       ├─ cmd/chat/{main.go,wire.go,wire_gen.go}
│       ├─ configs/{config.yaml,config.dev.yaml}
│       └─ internal/
│           ├─ conf/
│           ├─ server/
│           │   ├─ httpserver.go            # Gin REST
│           │   ├─ wsserver.go              # WebSocket 网关
│           │   ├─ grpcserver.go            # 内部/未来拆分预留
│           │   └─ consumer.go              # Kafka 消费者（消息投递）
│           ├─ service/                     # conversation / message / group / friend / gateway
│           ├─ biz/                         # ★ 领域层（纯业务，零框架依赖）
│           │   ├─ conversation/            # 会话：单聊/群聊统一模型
│           │   ├─ message/                 # 消息：发送/拉取/seq/幂等/outbox
│           │   ├─ group/                   # 群组：建群/拉人/退群/成员
│           │   ├─ friend/                  # 好友：申请/同意/拉黑/关系
│           │   ├─ presence/                # 在线状态：多端/心跳/路由
│           │   └─ delivery/                # 投递：网关路由与下行编排
│           └─ data/
│               ├─ model/                   # GORM 实体
│               ├─ repo/                    # Repository 实现
│               └─ client/                  # account gRPC 客户端封装、Kafka、Redis
│
├─ pkg/                                     # 可被任意服务引用的基建库（无业务语义）
│   ├─ xerr/         统一错误码 + errors 封装
│   ├─ xgin/         Gin 中间件（recover/trace/log/error/auth/ratelimit/cors）
│   ├─ xtransport/   Gin 的 transport.Server 适配器
│   ├─ xauth/        JWT 签发/校验 + ctx 用户注入
│   ├─ xkafka/       生产者(acks=all/幂等) + 消费者(手动提交/重试/DLQ)
│   ├─ xredis/       客户端 + redsync 锁 + 限流器
│   ├─ xws/          连接管理（读写泵/心跳/缓冲/优雅关闭/多端）
│   ├─ xid/          雪花 ID / UUID
│   ├─ xtrace/       OTel 初始化
│   ├─ xlog/         日志初始化
│   └─ xtest/        testcontainers 夹具
│
├─ deploy/
│   ├─ docker-compose.yaml                  # 本地基建 + 应用
│   ├─ Dockerfile.{account,chat}
│   ├─ k8s/                                 # Deployment/Service/HPA（预留）
│   └─ init/                                # Kafka topic、DB 迁移 Job
│
├─ docs/
│   ├─ architecture.md                      # 架构与领域模型
│   ├─ api.md                               # 由 OpenAPI 生成
│   ├─ deploy.md                            # 部署运维
│   └─ troubleshooting.md                   # 故障排查
│
├─ migrations/                              # golang-migrate 版本化 SQL
│   ├─ account/0001_init.up.sql / .down.sql
│   └─ chat/0001_init.up.sql / .down.sql
│
├─ Makefile
├─ .golangci.yml
├─ .env.example
├─ go.mod
└─ README.md
```

### 2.4 Kratos 与 Gin 的结合方式（关键技术方案）

Kratos 的 `transport` 层是插件化的，`Server` 接口只有 `Start/Stop` 两个方法，因此可以**把 Gin 引擎包装成一个标准的 Kratos Server**，从而同时获得：

- Gin 的路由与中间件生态（符合本次技术栈要求）
- Kratos 的应用生命周期、优雅启停、服务注册、Trace/Metrics 采集

```go
// pkg/xtransport/gin.go（核心适配器，约 60 行）
type Server struct {
    *gin.Engine
    httpSrv *http.Server
    addr    string
}

func (s *Server) Start(ctx context.Context) error { /* ListenAndServe，过滤 ErrServerClosed */ }
func (s *Server) Stop(ctx context.Context) error  { /* httpSrv.Shutdown(ctx) 优雅排空 */ }

// app/chat/cmd/chat/main.go
app := kratos.New(
    kratos.Name("chat"),
    kratos.Server(ginSrv, wsSrv, grpcSrv),  // 多传输同生命周期
    kratos.BeforeStop(/* 关闭 Kafka 消费者、排空 WS 连接 */),
)
```

参考：[Kratos 官方 Gin 示例](https://github.com/go-kratos/kratos/blob/main/examples/http/gin/main.go)、[Kratos Transport 抽象](https://go-kratos.dev/docs/component/transport/overview/)。

**请求链路**：`Gin Handler → middleware(xgin) → service 层 → biz 层 → data 层(Repo)`。
`service` 层只做参数绑定/转换与错误映射，**不含业务判断**。

---

## 3. 领域模型与数据设计（chat）

### 3.1 表结构

```sql
-- 会话：单聊与群聊统一建模
CREATE TABLE `conversation` (
  `id`            BIGINT UNSIGNED NOT NULL,          -- 雪花 ID
  `type`          TINYINT        NOT NULL,           -- 1=单聊 2=群聊
  `biz_key`       VARCHAR(191)   NOT NULL,           -- 单聊: "minUid_maxUid"；群聊: 雪花串
  `name`          VARCHAR(191)   NOT NULL DEFAULT '',
  `avatar_url`    VARCHAR(512)   NOT NULL DEFAULT '',
  `status`        TINYINT        NOT NULL DEFAULT 1, -- 1=正常 2=待确认 3=拉黑
  `owner_id`      BIGINT UNSIGNED NOT NULL DEFAULT 0,
  `max_seq`       BIGINT         NOT NULL DEFAULT 0, -- 会话内最新序号
  `extra`         JSON           NULL,
  `created_at`    DATETIME(3)    NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  `updated_at`    DATETIME(3)    NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_type_bizkey` (`type`, `biz_key`),
  KEY `idx_owner` (`owner_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 会话成员（含已读位点）
CREATE TABLE `conversation_member` (
  `id`              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `conversation_id` BIGINT UNSIGNED NOT NULL,
  `user_id`         BIGINT UNSIGNED NOT NULL,
  `alias_name`      VARCHAR(191)   NOT NULL DEFAULT '',
  `role`            TINYINT        NOT NULL DEFAULT 0,   -- 0=成员 1=管理员 2=群主
  `last_read_seq`   BIGINT         NOT NULL DEFAULT 0,
  `mute`            TINYINT        NOT NULL DEFAULT 0,
  `joined_at`       DATETIME(3)    NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_conv_user` (`conversation_id`, `user_id`),  -- 幂等加入
  KEY `idx_user_conv` (`user_id`, `conversation_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 消息（按会话+seq 有序；预留分区/分表）
CREATE TABLE `message` (
  `id`              BIGINT UNSIGNED NOT NULL,           -- 雪花 ID
  `conversation_id` BIGINT UNSIGNED NOT NULL,
  `seq`             BIGINT          NOT NULL,           -- 会话内单调递增
  `sender_id`       BIGINT UNSIGNED NOT NULL,
  `type`            TINYINT         NOT NULL DEFAULT 1, -- 1文本 2图片 3视频 4音频 5系统
  `content`         TEXT            NULL,
  `extra`           JSON            NULL,
  `client_msg_id`   VARCHAR(64)     NOT NULL,           -- 幂等键
  `status`          TINYINT         NOT NULL DEFAULT 1, -- 1正常 2撤回 3删除
  `created_at`      DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_conv_seq` (`conversation_id`, `seq`),          -- 顺序权威
  UNIQUE KEY `uk_conv_clientmsg` (`conversation_id`, `sender_id`, `client_msg_id`), -- 幂等
  KEY `idx_conv_created` (`conversation_id`, `created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 好友关系（独立于会话，语义更清晰）
CREATE TABLE `friend_relation` (
  `id`         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `user_id`    BIGINT UNSIGNED NOT NULL,
  `friend_id`  BIGINT UNSIGNED NOT NULL,
  `remark`     VARCHAR(191) NOT NULL DEFAULT '',
  `status`     TINYINT      NOT NULL DEFAULT 1,   -- 1正常 2拉黑
  `created_at` DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_user_friend` (`user_id`, `friend_id`),
  KEY `idx_friend` (`friend_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 好友申请（替代旧实现里 "message.type=0" 的 hack）
CREATE TABLE `friend_request` (
  `id`          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `from_uid`    BIGINT UNSIGNED NOT NULL,
  `to_uid`      BIGINT UNSIGNED NOT NULL,
  `apply_msg`   VARCHAR(255) NOT NULL DEFAULT '',
  `status`      TINYINT      NOT NULL DEFAULT 0,  -- 0待处理 1已同意 2已拒绝 3过期
  `expire_at`   DATETIME(3)  NOT NULL,
  `handled_at`  DATETIME(3)  NULL,
  `created_at`  DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_from_to_pending` (`from_uid`, `to_uid`, `status`),
  KEY `idx_to_status` (`to_uid`, `status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Outbox：本地消息表，保证"落库即投递"
CREATE TABLE `message_outbox` (
  `id`           BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `event_id`     VARCHAR(64)  NOT NULL,
  `topic`        VARCHAR(64)  NOT NULL,
  `partition_key` VARCHAR(64) NOT NULL,       -- conversation_id，保证同会话同分区
  `payload`      MEDIUMBLOB   NOT NULL,
  `status`       TINYINT      NOT NULL DEFAULT 0, -- 0待投递 1已投递 2失败
  `retry_count`  INT          NOT NULL DEFAULT 0,
  `next_retry_at` DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  `created_at`   DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_event` (`event_id`),
  KEY `idx_status_retry` (`status`, `next_retry_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

**与旧模型的关键差异**

| 旧 | 新 | 收益 |
| --- | --- | --- |
| `group` 表同时充当"会话"和"群" | `conversation` 语义明确 | 概念清晰 |
| 单聊 `groupId = "uid1_uid2"` 字符串 | `biz_key` 仅做业务去重，主键为雪花 ID | 避免字符串主键、便于分表 |
| 消息游标用全局自增 `id` | 会话级 `seq` | **同会话严格有序**，支持分表 |
| 好友申请塞进 `chat_msg(type=0)` | 独立 `friend_request` 表 | 语义正确，可查询可统计 |
| 无投递保障 | `message_outbox` | 落库与投递原子化 |
| 无已读位点 | `conversation_member.last_read_seq` | 支持未读数/已读回执 |

### 3.2 Redis Key 设计

| Key | 类型 | 用途 | TTL |
| --- | --- | --- | --- |
| `im:seq:{convId}` | String | 会话 seq 分配（INCR） | 24h（DB `max_seq` 可回填） |
| `im:route:{uid}` | Hash | 用户在线的网关节点列表（多端） | 由心跳续期 |
| `im:node:{nodeId}` | String | 网关节点存活 | 30s |
| `im:presence:{uid}` | String | 在线状态 | 60s，心跳续期 |
| `im:last_read:{convId}:{uid}` | String | 已读位点缓存 | 1h |
| `im:user:profile:{uid}` | Hash | account 用户资料快照 | 10min |
| `im:lock:conv:{bizKey}` | String | 会话创建锁（redsync） | 10s |
| `im:ratelimit:msg:{uid}` | String | 发送限流令牌桶 | 1s |

---

## 4. 核心链路设计

### 4.1 发送消息（可靠性 + 顺序性）

```
① 客户端 POST /api/v1/message/send  (Header: Authorization, clientMsgId)
        │
② Gin → service → biz/message.SendUseCase
        │
③ 幂等检查：Redis SetNX(im:idem:{convId}:{sender}:{clientMsgId})
        │
④ 单事务（DB）：
     · INSERT message (seq = Redis INCR im:seq:{convId})   ← 顺序权威
     · UPDATE conversation.max_seq
     · INSERT message_outbox (partition_key = convId)      ← 投递保障
        │
⑤ Outbox Relay（后台协程，100ms 轮询 / 或 CDC）：
     · SELECT ... FOR UPDATE SKIP LOCKED WHERE status=0
     · Kafka Produce(topic=im_msg, key=convId, acks=all, idempotent)
     · UPDATE outbox.status=1
        │
⑥ Kafka 消费者（每个 chat 实例独立 group = nodeId，全量消费）
     · 命中本地在线连接 → 写入 conn.SendChan
     · 未在线 → 不处理（离线消息由 seq 拉取保证）
        │
⑦ WS writePump → 客户端；客户端按 seq 校验，缺失则调 /message/sync 补洞
```

**可靠性的三个保证**

1. **不丢**：DB 事务 + Outbox（落库必投递）；Kafka `acks=all` + 幂等生产；消费成功才提交 offset；失败重试后进 DLQ。
2. **不重**：`uk_conv_clientmsg` 唯一索引 + Redis 幂等键双保险；消费端以 `(convId, seq)` 去重。
3. **有序**：同 `conversationId` 作为 Kafka 分区键 → 同分区单消费者串行；`seq` 单调递增 → 客户端可排序、可补洞。

### 4.2 多节点推送（替代 Redis Pub/Sub）

```
               ┌──────────── Kafka topic: im_msg（N 分区）────────────┐
               │  key = conversationId → 同会话固定分区                 │
               └───────┬─────────────────────────┬────────────────────┘
                       │                         │
        group = chat-node-A            group = chat-node-B     ← 每实例独立消费组
        消费全量，只推本地在线用户        消费全量，只推本地在线用户
```

- 每个网关实例使用**独立 consumer group**（group id = nodeId），因此都能消费到全量消息。
- 实例只对**本机持有连接**的用户投递，路由查 `im:route:{uid}`。
- 对比旧方案的优势：Kafka 有持久化与 offset，实例重启后可追溯；不存在 Pub/Sub 的 at-most-once 丢失。

### 4.3 离线同步

```
客户端重连 → GET /api/v1/message/sync?conversationId=&fromSeq=&limit=100
→ biz/message.SyncUseCase → 按 (convId, seq > fromSeq) 升序分页
→ 返回 {list, hasMore, maxSeq}
客户端记录 maxSeq，作为下次 fromSeq
```

### 4.4 好友流程（去 hack 化）

```
申请：POST /api/v1/friend/apply {toUid, msg}
      → biz/friend.ApplyUseCase：校验非好友/无 pending → INSERT friend_request
      → 通过 Kafka 发系统通知消息给 toUid

同意：POST /api/v1/friend/accept {requestId}
      → 单事务：UPDATE friend_request.status=1
                INSERT friend_relation ×2（双向）
                INSERT conversation(type=1, biz_key="min_max")
                INSERT conversation_member ×2
                INSERT message(type=5 系统问候)
```

---

## 5. 接口兼容策略

**原则：现有 HTTP 路径与原语义保持不变，字段只增不减。**

### 5.1 接口映射表（旧 → 新）

| 旧接口（go-zero） | 新接口（Kratos+Gin） | 变更 |
| --- | --- | --- |
| `POST /api/user/register` | `account: POST /api/user/register` | 路径不变 |
| `POST /api/user/login` | `account: POST /api/user/login` | 路径不变 |
| `POST /api/user/personal_info` | `account: POST /api/user/personal_info` | 路径不变 |
| `POST /api/user/query_user_info` | `account: POST /api/user/query_user_info` | 路径不变 |
| `POST /api/user/reset_password` | `account: POST /api/user/reset_password` | 路径不变 |
| `POST /api/user/modify_personal_info` | `account: POST /api/user/modify_personal_info` | 路径不变 |
| `POST /api/group/add_friend` | `chat: POST /api/group/add_friend` | 路径不变，返回增加 `requestId` |
| `POST /api/group/handle_friend` | `chat: POST /api/group/handle_friend` | 参数兼容 `groupId` → 新增 `requestId` |
| `POST /api/group/group_user_list` | `chat: POST /api/group/group_user_list` | 路径不变 |
| `POST /api/group/message_group_info_list` | `chat: POST /api/group/message_group_info_list` | 路径不变，`lastMsg` 增加 `seq` |
| `POST /api/group/create_group_chat` | `chat: POST /api/group/create_group_chat` | 路径不变 |
| `POST /api/group/add_group_chat` | `chat: POST /api/group/add_group_chat` | 路径不变 |
| `POST /api/message/upload` | `chat: POST /api/message/upload` | 路径不变，新增 `clientMsgId` 可选 |
| `POST /api/message/pull` | `chat: POST /api/message/pull` | 保留，新增 `fromSeq` 模式 |
| `GET /ws` | `chat: GET /ws` | 路径不变，`platform` Header 保留 |
| 统一响应 `{code,msg,data}` | 保持 | 错误码沿用 `400x/500x` |

### 5.2 新增接口（不破坏兼容）

| 接口 | 用途 |
| --- | --- |
| `POST /api/message/sync` | 按 seq 增量同步（补洞/重连） |
| `POST /api/message/read` | 上报已读位点 |
| `POST /api/group/quit` | 退出群聊 |
| `POST /api/group/member_list` | 群成员详情（含昵称/头像/角色） |
| `GET  /api/v1/healthz` `/readyz` | 健康检查（K8s 探针） |
| `GET  /metrics` | Prometheus 指标 |

### 5.3 契约测试

为每个旧接口准备 **请求/响应黄金样例**（fixture JSON），重构后跑一遍对比，字段与语义不一致即失败。这是"功能与它一致"的**唯一客观验收手段**。

---

## 6. 实施阶段（9 个 Phase）

> 每个 Phase 完成后必须**可运行、可验收**，不做"半年不落地"的大爆炸式改造。

### Phase 0：基线冻结与决策确认　✅ 已完成
- **完成**：切换基线分支 `rebuild/kratos-gin`；旧接口契约基线见 `README.md` §5。
- **说明**：按 v1.1 决策**不备份旧代码**；旧表结构与存量数据不作参考，数据库从零设计。
- **验收**：✅ 已完成。

### Phase 1：工程骨架　✅ 已完成
- **产出**：三 module 骨架（`pkg` / `Account` / `Chat`）、`go.work`、`Makefile`、`.env.example`、`deploy/docker-compose.local.yaml`（MySQL/Redis/Kafka-KRaft/etcd/Jaeger）、`deploy/init/mysql` 建库脚本、两套 `migrations/`、Wire 装配、Gin-as-Kratos-Transport 适配器、`/healthz` + `/readyz`。
- **实际验收结果**：
  - 三 module `go build ./...` 与 `go vet ./...` 全部退出码 0；`gofmt -l` 无输出。
  - Account：注册 / 登录 / 本人资料 / 查询他人 / 改密 / 改资料 六个接口 + JWT 鉴权 + 参数校验 + 错误码映射，**11 项端到端用例全部通过**（含重复注册 4004、弱密码 4001、非法邮箱 4002、无 token 4001）。
  - Chat：`/healthz`、`/readyz`、占位路由 `/api/chat/ping` 全部正常。
  - `/readyz` 对 MySQL 与 Redis 的真实探测均返回 `ok`。
- **顺延项**：Jaeger 链路与 Prometheus 指标已预留配置项，实际接入放在 Phase 7；Chat 的 gRPC 服务端与业务层放在 Phase 4~6。

### Phase 2：pkg 基建库　❌ 已作废
> 与 v1.1「`pkg` 只放协议」冲突，见 v1.2 第 4 条。公共基建改为在 `Account/internal` 与 `Chat/internal` 内各自实现，两边不共享。后续阶段编号顺延。
- **产出**：`xerr / xgin / xauth / xredis / xkafka / xws / xid / xtrace / xlog / xtest`。
- **验收**：单测覆盖率 ≥ 80%；`xkafka` 有故障注入测试（broker 不可用时行为可预期）。

### Phase 3：account 应用
- **产出**：`account.v1` proto 全量方法 + biz/user + data + Gin handlers（保留旧路径）。
- **验收**：契约测试全绿；bcrypt 兼容旧密码哈希（旧用户可直接登录）。

### Phase 4：chat —— 会话 / 好友 / 群组
- **产出**：conversation/member/friend_request/friend_relation 模型与用例；依赖 `account.BatchGetUser`（带缓存）补全昵称头像。
- **验收**：加好友 → 同意 → 建会话 → 会话列表 全链路契约测试通过。

### Phase 5：chat —— 消息
- **产出**：seq 分配器、幂等发送、Outbox Relay、Kafka 生产(acks=all)、按 seq 拉取/同步。
- **验收**：并发压测下**无丢无重、同会话严格有序**；注入 Kafka 故障后消息不丢（Outbox 补偿）。

### Phase 6：chat —— WS 网关与多节点
- **产出**：WS 连接管理（心跳/多端/优雅排空）、路由表、独立 consumer group 投递。
- **验收**：双实例部署跨节点投递 100%；`kill -9` 任一实例，客户端重连后消息完整（靠 seq 补洞）。

### Phase 7：稳定性与可观测
- **产出**：Prometheus 指标（发送量/投递延迟/在线数/Kafka lag/WS 连接数）、限流、熔断、Grafana 看板、告警规则。
- **验收**：看板可用；限流阈值生效；熔断在依赖不可用时按预期降级。

### Phase 8：测试与质量
- **产出**：单测 / testcontainers 集成测试 / 契约测试 / k6 压测脚本 / CI 流水线（lint+test+build+image）。
- **验收**：CI 全绿；压测报告达标（见第 7 节指标）。

### Phase 9：清理与交付
- **产出**：删除旧 `app/` `common/` `proto/` `vendor/` `init.sql` `start*.sh` `Dockerfile` `docker-compose.yml` `filebeat.yml` 及旧 `go.mod`/`go.sum`；重写 `README.md`；补齐 `docs/`；SAE 部署清单。
- **验收**：仓库无残留 go-zero 代码；新人按 README 可在 10 分钟内跑通全链路。

---

## 7. 质量目标（可量化验收）

| 维度 | 目标 |
| --- | --- |
| 消息可靠性 | 故障注入（Kafka 宕机 30s / 网关 kill）下 **丢消息数 = 0** |
| 消息顺序性 | 同会话 seq 严格单调，客户端 0 乱序 |
| 发送延迟 | P99 < 100ms（同机房，不含客户端网络） |
| 投递延迟 | P99 < 200ms（发出 → 对端收到） |
| 单实例连接 | ≥ 10,000 WS 并发连接稳定 30 分钟，内存无泄漏 |
| 消息吞吐 | ≥ 5,000 msg/s（集群，3 实例） |
| 接口兼容 | 旧接口契约测试 **100% 通过** |
| 测试覆盖 | `biz` 层单测 ≥ 80%，整体 ≥ 70% |
| 可观测 | 100% 请求带 traceId；核心指标 5 个看板 + 8 条告警 |
| 代码质量 | `golangci-lint` 零告警；无 `panic()` 兜底；无全局可变状态 |

---

## 8. 风险与对策

| 风险 | 影响 | 对策 |
| --- | --- | --- |
| 重构期间功能回归 | 高 | Phase 0 冻结契约 + Phase 8 契约测试兜底 |
| 旧数据需迁移 | 中 | `migrations/` 提供数据搬迁脚本；`group`→`conversation` 映射明确；先双读校验再切换 |
| account 服务未就绪 | 中 | 先冻结 proto 契约 + 提供 mock server，chat 侧不阻塞 |
| Kafka/etcd 本地环境复杂 | 中 | docker-compose 一键起；`make dev-up` 封装 |
| 顺序性方案改动大 | 中 | seq 方案在 Phase 5 单独交付并压测验证，可回退到 id 游标 |
| 工作量巨大导致中途停滞 | 高 | 9 个 Phase 独立交付；每 Phase 有明确验收；优先保障 Phase 3/5/6 三大主链路 |
| 依赖版本不兼容 | 低 | Phase 1 锁定版本并提交 `go.sum`；CI 做依赖审计 |
| 旧 JWT 用户需重新登录 | 中 | `xauth` 兼容旧 HS256 + `uid` claim；如不可行则提供过渡期双校验 |

---

## 9. 团队协作与并行策略

- **可并行**：Phase 1（骨架）完成后，`pkg/` 与 `account`、`chat` 三大块可由不同执行单元并行推进。
- **强依赖链**：Phase 1 → Phase 2 → （Phase 3 ∥ Phase 4） → Phase 5 → Phase 6 → Phase 7 → Phase 8 → Phase 9。
- **代码规范**：所有新增代码必须通过 `make lint`；biz 层禁止 import gorm/gin/kratos。
- **提交约定**：Conventional Commits；每个 Phase 一个 PR，PR 描述附验收证据（截图/压测报告/测试输出）。

---

## 10. 交付物清单

| # | 交付物 | 位置 |
| --- | --- | --- |
| 1 | 可运行的 `account` 应用 | `app/account/` |
| 2 | 可运行的 `chat` 应用（核心） | `app/chat/` |
| 3 | 公共基建库（含测试） | `pkg/` |
| 4 | Proto 契约与生成物 | `api/` |
| 5 | 版本化数据库迁移 | `migrations/` |
| 6 | 本地编排与镜像 | `deploy/` |
| 7 | 压测脚本与报告 | `deploy/loadtest/` |
| 8 | 架构/接口/部署/排查文档 | `docs/` |
| 9 | 重写的 README | 根目录 |

---

## 11. 执行顺序与里程碑

```
M1 ── Phase 0 + 1        骨架跑通（/healthz + trace + 迁移）
M2 ── Phase 2            基建库就绪（含单测）
M3 ── Phase 3            account 全接口契约测试通过
M4 ── Phase 4            会话/好友/群组链路可用
M5 ── Phase 5            消息可靠有序（压测通过）★ 核心里程碑
M6 ── Phase 6            多节点 WS 推送可用 ★ 核心里程碑
M7 ── Phase 7 + 8        可观测 + 测试 + CI 达标
M8 ── Phase 9            旧代码清除，交付完成
```

---

## 12. 待确认决策点（评审时请逐条确认）

| # | 决策点 | 建议默认值 | 影响 |
| --- | --- | --- | --- |
| A | 仓库形态：单仓双应用（account + chat）还是仅 chat？ | **单仓双应用** | 决定工作量与是否需要 account |
| B | `account` 的 `/api/user/*` 接口是否保留在本仓库？ | **保留** | 决定接口兼容范围 |
| C | ORM 选型 | **GORM v2** | 影响 data 层全部实现 |
| D | Kafka 客户端 | **franz-go** | 影响 kafka 封装 |
| E | WS 库 | **gorilla/websocket** | 影响 ws 网关 |
| F | 是否保留 `vendor/` | **不保留** | 影响构建方式 |
| G | 用户昵称/头像获取方式 | **实时 gRPC + Redis 缓存** | 决定是否引入冗余快照表 |
| H | 消息 seq 分配方式 | **Redis INCR + DB 回填** | 影响顺序性实现 |
| I | 是否兼容旧 JWT Token | **兼容**（HS256 + uid claim） | 影响用户是否需要重新登录 |
| J | 是否迁移旧库存量数据 | **提供脚本，可选执行** | 影响迁移 Phase 0 工作量 |
| K | 旧代码处理方式 | **打 tag `legacy-go-zero-final` 后删除** | 影响可回滚性 |

---

## 13. 不在本次范围（明确排除）

以下为"日后扩展"，本次**不实现**，仅在架构上预留接口：

- 推送服务（APNS / FCM / 厂商推送）
- 文件/媒体服务（对象存储、转码、CDN）
- 消息搜索（ES 全文检索）
- 消息撤回 / 编辑 / 引用 / 转发
- 内容审核与风控
- OAuth2 / OIDC、多因素认证
- 冷热数据分层与归档
- 多地域部署与单元化

---

*本章程为重构的唯一依据。执行过程中若需偏离，必须先更新本文档并重新评审。*
