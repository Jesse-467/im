// Package conf 负责从环境变量装载本服务的全部运行时配置。
//
// 设计原则：
//   - 所有基础设施均由环境变量驱动，*_TYPE 决定具体实现，
//     从而支持「本地 docker 组件」与「云端（RDS/Tair/云 Kafka/Nacos）」无缝切换；
//   - 配置在启动时一次性加载并强校验，宁可启动失败，也不带着错误配置对外服务；
//   - 本包不依赖任何框架或第三方库，便于单元测试。
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
	ServiceName string // account

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

// 配置问题的严重级别。
//
// 区分「致命」与「可降级」的依据是：缺了它服务还能不能提供有价值的服务。
//   - 数据库、JWT 密钥缺失 → 任何业务请求都无法完成，属于致命；
//   - 缓存不可用 → 多设备在线上限的踢出能力降级（退化为不限制），
//     但登录、注册、资料查询仍可用，属于可降级。
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

// DB 关系型数据库配置。
//
// Type 取值：postgres（本地）| polardb-pg | rds-pg。
// 云端产品均兼容 PostgreSQL 协议，因此三者共用同一驱动与 DSN 形式，
// 差异只体现在地址、SSL 要求与连接参数上。
type DB struct {
	Type            string
	Host            string
	Port            int
	User            string
	Password        string
	Name            string
	SSLMode         string
	DSN             string // 显式指定时优先级最高
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
	Username      string
	Password      string
	ConsumerGroup string
}

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
	GRPCAddr        string
	JWTSecret       string
	JWTAccessExpire time.Duration

	// TokenMode 决定校验令牌时是否需要回查数据库。
	//
	//   - TokenModeSelf：只验证 JWT 自身的签名与过期时间。
	//     优点是零存储依赖、校验极快，且能脱离数据库横向扩展；
	//     代价是令牌在过期前无法被提前吊销（如改密后、被踢下线后仍可用）。
	//   - TokenModeDB：在 JWT 自校验之外，还要求数据库中该令牌未被吊销。
	//     支持即时吊销（踢下线、改密），代价是每次校验多一次存储查询。
	//
	// 默认使用 TokenModeDB：多设备在线上限功能依赖「踢出即失效」，
	// 若默认 self，被踢的设备在令牌自然过期前仍能继续使用。
	TokenMode string

	// MaxDevicesPerUser 是单账号允许同时在线（持有有效令牌）的设备数上限。
	//
	// 取值为 0 表示不限制。超限时按 Redis 有序集合的加入时间踢出最早的设备，
	// 并吊销其令牌。
	MaxDevicesPerUser int

	// OnlineDeviceTTL 是设备在线标记（Redis 有序集合）的兜底有效期。
	//
	// 有序集合本应由「退出登录 / 被踢出」显式清理，但客户端异常退出、
	// 进程被强杀等场景不会触发清理，长期运行会持续膨胀。
	// 因此给整个键一个较长的 TTL，由后续登录行为被动续期。
	OnlineDeviceTTL time.Duration
}

// TokenModeSelf 只校验 JWT 自身（签名 + 过期时间）。
const TokenModeSelf = "self"

// TokenModeDB 在 JWT 自校验之外，还要求令牌在数据库中未被吊销。
const TokenModeDB = "db"

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
		ServiceName: envString("ServiceName", "account"),

		DB: DB{
			Type: envString("DB_TYPE", "postgres"),
			// Host / Name 刻意不给默认值：它们决定「连到哪个库」，
			// 猜错会静默连到另一个数据库（本地开发尤其容易发生）。
			// 缺省时由 Check 明确报错，比带默认值安全得多。
			// Port / User 有行业通用默认值，给了不会掩盖配置错误。
			Host:            envString("DB_HOST", ""),
			Port:            envInt("DB_PORT", 5432),
			User:            envString("DB_USER", "postgres"),
			Password:        envString("DB_PASSWORD", ""),
			Name:            envString("DB_NAME", ""),
			SSLMode:         envString("DB_SSL_MODE", "disable"),
			DSN:             envString("DB_DSN", ""),
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
			Username:      envString("MQ_USERNAME", ""),
			Password:      envString("MQ_PASSWORD", ""),
			ConsumerGroup: envString("MQ_CONSUMER_GROUP", ""),
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
			HTTPAddr:        envString("HTTP_ADDR", "0.0.0.0:8001"),
			GRPCAddr:        envString("GRPC_ADDR", "0.0.0.0:9001"),
			JWTSecret:       envString("JWT_SECRET", ""),
			JWTAccessExpire: envDuration("JWT_ACCESS_EXPIRE", 86400*time.Second),
			// 默认 db 模式：多设备踢出依赖「吊销后立即失效」，
			// 若默认 self，被踢的设备在令牌自然过期前仍能继续使用。
			TokenMode:         envString("TOKEN_MODE", TokenModeDB),
			MaxDevicesPerUser: envInt("MAX_DEVICES_PER_USER", 3),
			OnlineDeviceTTL:   envDuration("ONLINE_DEVICE_TTL", 30*24*time.Hour),
		},
	}

	// 致命问题立即退出，可降级问题仅记录、由 main 在日志就绪后告警。
	//
	// 这样区分的原因：把所有依赖都当成硬依赖，会让任何一个非关键组件
	// （如缓存）的配置疏漏直接演变成整个服务无法启动，反而放大了故障面。
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
	if c.App.HTTPAddr == "" || c.App.GRPCAddr == "" {
		add(LevelFatal, "HTTP_ADDR 与 GRPC_ADDR 均不能为空")
	}
	if c.App.JWTSecret == "" {
		add(LevelFatal, "JWT_SECRET 不能为空")
	} else if c.App.JWTAccessExpire <= 0 {
		add(LevelFatal, "JWT_ACCESS_EXPIRE 必须大于 0")
	}
	if c.App.TokenMode != TokenModeSelf && c.App.TokenMode != TokenModeDB {
		add(LevelFatal, "TOKEN_MODE 取值非法 %q，可选 self|db", c.App.TokenMode)
	}
	if c.App.MaxDevicesPerUser < 0 {
		add(LevelFatal, "MAX_DEVICES_PER_USER 不能为负数，当前 %d", c.App.MaxDevicesPerUser)
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
		add(LevelWarn, "缓存未配置（CACHE_ADDRS 为空）：多设备在线上限将退化为不限制，"+
			"被踢设备无法即时失效")
	}
	// db 模式依赖数据库回查令牌状态，缓存不可用会让每次校验都落到数据库上，
	// 此时仍能工作（只是响应变慢），因此仅提示不阻断。
	if c.App.TokenMode == TokenModeDB && len(c.Cache.Addrs) == 0 {
		add(LevelWarn, "TOKEN_MODE=db 且缓存未配置：令牌校验将直接查询数据库，延迟会显著上升")
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
