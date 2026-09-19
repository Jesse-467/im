// Package conf 负责从环境变量装载 Chat（即时聊天服务）的全部运行时配置。
//
// 设计原则：
//   - 所有基础设施均由环境变量驱动，*_TYPE 决定具体实现，
//     从而支持「本地 docker 组件」与「云端（RDS/Tair/云 Kafka/Nacos）」无缝切换；
//   - 配置在启动时一次性加载并强校验，宁可启动失败，也不带着错误配置对外服务；
//   - Chat 与 Account 之间不做任何代码共享，因此本包是 Chat 独立维护的一份实现，
//     只通过 pkg 中的 gRPC 契约与账号中心交互。
package conf

import (
	"fmt"
	"strings"
	"time"
)

// 运行环境取值
const (
	EnvDev  = "dev"
	EnvTest = "test"
	EnvProd = "prod"
)

// TypeLogFallback 是 MQ_TYPE 未配置时使用的兜底实现名。
//
// 它只用于在配置告警里描述实际生效的行为；真正的实现选择在 mq 包内完成。
const TypeLogFallback = "log"

// weakSecrets 是不允许在生产环境使用的占位密钥。
var weakSecrets = map[string]struct{}{
	"":                        {},
	"change_me":               {},
	"change_me_in_production": {},
	"secret":                  {},
	"123456":                  {},
}

// Config 聚合本服务的全部运行时配置。
type Config struct {
	Environment string // dev | test | prod
	ServiceName string // chat

	DB       DB
	Cache    Cache
	MQ       MQ
	Registry Registry
	Trace    Trace
	Metrics  Metrics
	Log      Log
	App      App

	// degradations 记录「不阻断启动但会削弱能力」的配置问题。
	//
	// 装载阶段日志组件尚未就绪，无法直接输出告警，因此先暂存在这里，
	// 由 main 在初始化日志之后调用 ReportDegradations 输出。
	degradations []Issue
}

// Degradations 返回可降级的配置问题。
func (c *Config) Degradations() []Issue { return c.degradations }

// ReportDegradations 输出可降级配置的告警。
//
// 由 main 在日志组件就绪后调用。之所以不在 Load 里直接打日志：
// 那时 logger 还不存在，只能写标准错误，会绕过统一的日志格式与采集。
func (c *Config) ReportDegradations(logf func(format string, args ...any)) {
	for _, i := range c.degradations {
		logf("配置降级：%s", i.Message)
	}
}

// DB 关系型数据库配置。
//
// Type 取值：postgres（本地）| polardb-pg | rds-pg。
// 云端产品均兼容 PostgreSQL 协议，因此三者共用同一驱动与 DSN 形式，
// 差异只体现在地址、SSL 要求与连接参数上。
type DB struct {
	Type     string
	Host     string
	Port     int
	User     string
	Password string
	Name     string
	SSLMode  string
	DSN      string // 显式指定时优先级最高
	// DebugSQL 开启后会打印每一条 SQL，仅用于本地排障。
	// 默认关闭：后台轮询任务会高频产生 SQL，打开后会淹没真正的错误日志。
	DebugSQL        bool
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

// Configured 判断数据库是否已配置。
//
// 不能拿 EffectiveDSN() 是否为空来判断：它总是返回拼接后的字符串，
// 即使各字段全为空也会产出一串 "host= port=0 user= ..." 的无效 DSN，
// 于是该校验形同虚设，服务会带着空地址启动、直到第一次查询才失败。
func (d DB) Configured() bool {
	if d.DSN != "" {
		return true
	}
	return d.Host != "" && d.Name != ""
}

// EffectiveDSN 返回最终使用的 DSN。
//
// 采用 PostgreSQL 的关键字/值形式而非 URL 形式：口令中常含 @ : / 等字符，
// URL 形式需要转义，关键字形式则天然安全。
func (d DB) EffectiveDSN() string {
	if d.DSN != "" {
		return d.DSN
	}
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s TimeZone=Asia/Shanghai connect_timeout=5",
		d.Host, d.Port, d.User, d.Password, d.Name, d.SSLMode,
	)
}

// Cache 缓存配置。
//
// Type 取值：redis-standalone（本地）| redis-cluster | redis-sentinel | tair。
type Cache struct {
	Type     string
	Addrs    []string
	Password string
	DB       int
	PoolSize int
	// MasterName 仅哨兵模式使用，指定主节点名称。
	MasterName string
}

// MQ 消息队列配置。
//
// Type 取值：kafka（本地）| aliyun-kafka | rocketmq。
type MQ struct {
	Type          string
	Brokers       []string
	TopicPrefix   string
	TopicMessage  string
	Username      string
	Password      string
	ConsumerGroup string
	// TLS 表示是否启用 TLS 连接。云 Kafka 通常强制要求，本地明文部署则关闭。
	TLS bool
}

// MessageTopic 用「主题前缀 + 领域名」拼出完整 topic 名。
//
// 不直接把完整 topic 名写死在代码里，是为了让同一份制品能在 dev/test/prod
// 之间复用：只切换 MQ_TOPIC_PREFIX 即可完成环境隔离，避免跨环境串消息。
func (m MQ) MessageTopic() string { return m.TopicPrefix + "." + m.TopicMessage }

// Registry 注册中心配置。
//
// Type 取值：etcd（本地）| nacos | none。
type Registry struct {
	Type      string
	Addrs     []string
	Namespace string
}

// Enabled 表示是否启用服务注册与发现。
func (r Registry) Enabled() bool { return r.Type != "" && r.Type != "none" }

// Trace 链路追踪配置。
type Trace struct {
	Enabled      bool
	Endpoint     string
	SamplerRatio float64
}

// Metrics 指标配置。
type Metrics struct {
	Enabled bool
	Path    string
}

// Log 日志配置。Format 取值：json | text。
type Log struct {
	Level  string
	Format string
}

// App 应用自身配置。
type App struct {
	HTTPAddr        string
	JWTSecret       string
	JWTAccessExpire time.Duration
	// WSAddr 是 WebSocket 网关对外声明的地址。
	//
	// 网关本身不独立监听端口（挂载在 HTTP 引擎上），该地址用于
	// 生成对客户端下发的连接地址，以及服务注册时的 Endpoint。
	WSAddr string
	// WSPath 是 WebSocket 的挂载路径
	WSPath string
	// AccountRPCEndpoint 是 Chat 依赖的账号中心 gRPC 地址。
	//
	// 聊天服务只通过 gRPC 契约消费账号能力（如用户信息查询），
	// 因此这里保存的是「下游依赖地址」而非本服务的监听地址。
	AccountRPCEndpoint string

	// NodeID 是雪花 ID 生成器使用的节点号。
	//
	// 它描述「本实例是谁」而非某个外部依赖，因此归属 App 而非独立分组；
	// 取值 0 表示按主机名哈希推导，容器环境下建议显式注入（如取 StatefulSet 序号），
	// 否则宿主名随机会导致重启后节点号漂移。
	NodeID int64
}

// IsProd 是否为生产环境。
func (c *Config) IsProd() bool { return c.Environment == EnvProd }

// IsDev 是否为本地开发环境。
func (c *Config) IsDev() bool { return c.Environment == EnvDev }

// Debug 表示是否开启调试能力（gRPC reflection、pprof、调试路由）。
func (c *Config) Debug() bool { return !c.IsProd() }

// Load 从环境变量装载配置并完成校验。
//
// 若当前目录存在 .env 文件，会先加载它（已存在的真实环境变量优先，不会被覆盖），
// 方便本地开发；生产环境通常没有该文件，行为不受影响。
func Load() (*Config, error) {
	loadDotEnv(".env")

	c := &Config{
		Environment: envString("Environment", EnvDev),
		ServiceName: envString("ServiceName", "chat"),

		DB: DB{
			Type: envString("DB_TYPE", "postgres"),
			// Host / Name 刻意不给默认值：它们决定「连到哪个库」，
			// 猜错会静默连到另一个数据库（本地开发尤其容易发生）。
			// 缺省时由 Validate 明确报错，比带默认值安全得多。
			// Port / User 有行业通用默认值，给了不会掩盖配置错误。
			Host:            envString("DB_HOST", ""),
			Port:            envInt("DB_PORT", 5432),
			User:            envString("DB_USER", "postgres"),
			Password:        envString("DB_PASSWORD", ""),
			Name:            envString("DB_NAME", ""),
			SSLMode:         envString("DB_SSL_MODE", "disable"),
			DSN:             envString("DB_DSN", ""),
			DebugSQL:        envBool("DB_DEBUG_SQL", false),
			MaxOpenConns:    envInt("DB_MAX_OPEN_CONNS", 100),
			MaxIdleConns:    envInt("DB_MAX_IDLE_CONNS", 20),
			ConnMaxLifetime: envDuration("DB_CONN_MAX_LIFETIME", time.Hour),
		},

		Cache: Cache{
			Type: envString("CACHE_TYPE", "redis-standalone"),
			// Addrs 同样不给默认值：缓存虽然可降级，但「悄悄连到默认地址」
			// 会让降级告警永远不触发，运维也就无从发现有组件没配好。
			Addrs:      envStringSlice("CACHE_ADDRS", nil),
			Password:   envString("CACHE_PASSWORD", ""),
			DB:         envInt("CACHE_DB", 0),
			PoolSize:   envInt("CACHE_POOL_SIZE", 100),
			MasterName: envString("CACHE_MASTER_NAME", ""),
		},

		MQ: MQ{
			Type:          envString("MQ_TYPE", "kafka"),
			Brokers:       envStringSlice("MQ_BROKERS", []string{"127.0.0.1:9092"}),
			TopicPrefix:   envString("MQ_TOPIC_PREFIX", "im"),
			TopicMessage:  envString("MQ_TOPIC_MESSAGE", "message"),
			Username:      envString("MQ_USERNAME", ""),
			Password:      envString("MQ_PASSWORD", ""),
			ConsumerGroup: envString("MQ_CONSUMER_GROUP", ""),
			TLS:           envBool("MQ_TLS", false),
		},

		Registry: Registry{
			Type:      envString("REGISTRY_TYPE", "none"),
			Addrs:     envStringSlice("REGISTRY_ADDRS", []string{"127.0.0.1:2379"}),
			Namespace: envString("REGISTRY_NAMESPACE", "im"),
		},

		Trace: Trace{
			Enabled:      envBool("TRACE_ENABLED", false),
			Endpoint:     envString("TRACE_ENDPOINT", ""),
			SamplerRatio: envFloat("TRACE_SAMPLER_RATIO", 1.0),
		},

		Metrics: Metrics{
			Enabled: envBool("METRICS_ENABLED", true),
			Path:    envString("METRICS_PATH", "/metrics"),
		},

		Log: Log{
			Level:  envString("LOG_LEVEL", "info"),
			Format: envString("LOG_FORMAT", "json"),
		},

		App: App{
			HTTPAddr:        envString("HTTP_ADDR", "0.0.0.0:8002"),
			JWTSecret:       envString("JWT_SECRET", ""),
			JWTAccessExpire: envDuration("JWT_ACCESS_EXPIRE", 86400*time.Second),
			WSAddr:          envString("WS_ADDR", "127.0.0.1:8002"),
			WSPath:          envString("WS_PATH", "/ws"),
			// AccountRPCEndpoint 不给默认值：它是 Chat 的强依赖，
			// 猜一个地址只会把「配置缺失」变成「运行期连不上」，
			// 而后者要在第一次调用账号中心时才会暴露。
			AccountRPCEndpoint: envString("ACCOUNT_RPC_ENDPOINT", ""),
			NodeID:             int64(envInt("NODE_ID", 0)),
		},
	}

	// 致命问题立即退出，可降级问题仅记录、由 main 在日志就绪后告警。
	//
	// 这样区分的原因：把所有依赖都当成硬依赖，会让任何一个非关键组件
	// （如缓存、消息队列）的配置疏漏直接演变成整个服务无法启动，
	// 反而放大了故障面。
	issues := c.Check()
	var fatal []Issue
	for _, i := range issues {
		if i.IsFatal() {
			fatal = append(fatal, i)
		} else {
			c.degradations = append(c.degradations, i)
		}
	}
	if len(fatal) > 0 {
		msgs := make([]string, 0, len(fatal))
		for _, i := range fatal {
			msgs = append(msgs, i.Message)
		}
		return nil, fmt.Errorf("conf: 配置存在致命问题，服务拒绝启动：\n  - %s",
			strings.Join(msgs, "\n  - "))
	}
	return c, nil
}

// 配置问题的严重级别。
//
// 区分「致命」与「可降级」的依据是：缺了它服务还能不能提供有价值的服务。
//   - 数据库、账号中心地址缺失 → 任何业务请求都无法完成，属于致命；
//   - 缓存、消息队列不可用 → 服务能启动并接受请求，只是能力降级
//     （如推送链路断开、序号分配退化），应当告警但不阻断启动。
//
// 之所以不做成「一律拒绝启动」：把所有依赖都设为硬依赖，会让任何一个
// 非关键组件抖动都演变成整个服务不可用，反而降低可用性。
const (
	LevelFatal = "fatal"
	LevelWarn  = "warn"
)

// Issue 是一条配置问题。
type Issue struct {
	Level   string
	Message string
}

// IsFatal 判断是否为致命问题。
func (i Issue) IsFatal() bool { return i.Level == LevelFatal }

// Check 逐项检查配置，返回全部问题（不因首个问题而中断）。
//
// 一次性返回所有问题而不是遇到第一个就返回：部署时最怕的是
// 「改一个、重启一次、又报下一个」，逐项列全可以让配置一次改对。
func (c *Config) Check() []Issue {
	var issues []Issue

	add := func(level, format string, args ...any) {
		issues = append(issues, Issue{Level: level, Message: fmt.Sprintf(format, args...)})
	}

	// ── 致命：缺失后服务无法对外提供任何有意义的服务 ──

	switch c.Environment {
	case EnvDev, EnvTest, EnvProd:
	default:
		add(LevelFatal, "Environment 取值非法 %q，可选 dev|test|prod", c.Environment)
	}
	if c.ServiceName == "" {
		add(LevelFatal, "ServiceName 不能为空")
	}
	if !c.DB.Configured() {
		add(LevelFatal, "数据库未配置，请设置 DB_DSN 或 DB_HOST/DB_NAME")
	} else if c.DB.MaxOpenConns <= 0 {
		add(LevelFatal, "DB_MAX_OPEN_CONNS 必须大于 0，当前 %d", c.DB.MaxOpenConns)
	}
	if c.App.HTTPAddr == "" {
		add(LevelFatal, "HTTP_ADDR 不能为空")
	}
	if c.App.JWTSecret == "" {
		add(LevelFatal, "JWT_SECRET 不能为空")
	} else if c.App.JWTAccessExpire <= 0 {
		add(LevelFatal, "JWT_ACCESS_EXPIRE 必须大于 0")
	}
	// 账号中心是 Chat 的强依赖：身份资料、好友昵称头像都要经它取用。
	// 地址为空时任何涉及用户的请求都会失败，因此属于致命。
	if c.App.AccountRPCEndpoint == "" {
		add(LevelFatal, "ACCOUNT_RPC_ENDPOINT 不能为空")
	}

	// ── 致命：生产环境的安全底线 ──

	if c.IsProd() {
		if _, weak := weakSecrets[c.App.JWTSecret]; weak || len(c.App.JWTSecret) < 32 {
			add(LevelFatal, "生产环境 JWT_SECRET 必须是长度不小于 32 的强随机串")
		}
		if c.DB.Password == "" {
			add(LevelFatal, "生产环境 DB_PASSWORD 不能为空")
		}
	}

	// ── 可降级：缺失时服务仍可运行，仅相关能力受限 ──

	if len(c.Cache.Addrs) == 0 {
		add(LevelWarn, "缓存未配置（CACHE_ADDRS 为空）：消息序号分配与在线路由不可用，"+
			"发送消息将失败，实时推送与跨节点投递不可用")
	}
	// 非生产环境允许 log 投递（不依赖中间件），但要提醒它不是生产配置。
	// 生产环境使用 log 会在 mq.NewPublisher 里被直接拒绝，因此这里不重复拦截。
	if !c.IsProd() && strings.EqualFold(c.MQ.Type, TypeLogFallback) {
		add(LevelWarn, "MQ_TYPE=%s 只适用于本地开发与测试：消息不经过真实队列，"+
			"投递链路仅在本进程内闭环", c.MQ.Type)
	}

	return issues
}

// FatalIssues 返回全部致命问题，供启动时判定是否可以直接退出。
func (c *Config) FatalIssues() []Issue {
	var fatal []Issue
	for _, i := range c.Check() {
		if i.IsFatal() {
			fatal = append(fatal, i)
		}
	}
	return fatal
}

// Validate 校验配置，任一问题（含可降级项）都返回错误。
//
// 保留该方法是为了兼容单元测试与「严格模式」调用方；
// 运行时启动路径请用 Check + FatalIssues，以便把可降级项降为告警。
func (c *Config) Validate() error {
	issues := c.Check()
	if len(issues) == 0 {
		return nil
	}
	msgs := make([]string, 0, len(issues))
	for _, i := range issues {
		msgs = append(msgs, fmt.Sprintf("[%s] %s", i.Level, i.Message))
	}
	return fmt.Errorf("conf: %s", strings.Join(msgs, "; "))
}
