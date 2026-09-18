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
}

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
		ServiceName: envString("ServiceName", "account"),

		DB: DB{
			Type:            envString("DB_TYPE", "postgres"),
			Host:            envString("DB_HOST", "127.0.0.1"),
			Port:            envInt("DB_PORT", 5432),
			User:            envString("DB_USER", "postgres"),
			Password:        envString("DB_PASSWORD", ""),
			Name:            envString("DB_NAME", "im_account"),
			SSLMode:         envString("DB_SSL_MODE", "disable"),
			DSN:             envString("DB_DSN", ""),
			MaxOpenConns:    envInt("DB_MAX_OPEN_CONNS", 100),
			MaxIdleConns:    envInt("DB_MAX_IDLE_CONNS", 20),
			ConnMaxLifetime: envDuration("DB_CONN_MAX_LIFETIME", time.Hour),
		},

		Cache: Cache{
			Type:       envString("CACHE_TYPE", "redis-standalone"),
			Addrs:      envStringSlice("CACHE_ADDRS", []string{"127.0.0.1:6379"}),
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
		},
	}

	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// Validate 校验配置的完整性与安全性。
func (c *Config) Validate() error {
	switch c.Environment {
	case EnvDev, EnvTest, EnvProd:
	default:
		return fmt.Errorf("conf: Environment 取值非法 %q，可选 dev|test|prod", c.Environment)
	}

	if c.ServiceName == "" {
		return fmt.Errorf("conf: ServiceName 不能为空")
	}
	if c.DB.EffectiveDSN() == "" {
		return fmt.Errorf("conf: 数据库未配置，请设置 DB_DSN 或 DB_HOST/DB_NAME")
	}
	if c.DB.MaxOpenConns <= 0 {
		return fmt.Errorf("conf: DB_MAX_OPEN_CONNS 必须大于 0，当前 %d", c.DB.MaxOpenConns)
	}
	if len(c.Cache.Addrs) == 0 {
		return fmt.Errorf("conf: 缓存未配置，请设置 CACHE_ADDRS")
	}
	if c.App.HTTPAddr == "" || c.App.GRPCAddr == "" {
		return fmt.Errorf("conf: HTTP_ADDR 与 GRPC_ADDR 均不能为空")
	}
	if c.App.JWTSecret == "" {
		return fmt.Errorf("conf: JWT_SECRET 不能为空")
	}
	if c.App.JWTAccessExpire <= 0 {
		return fmt.Errorf("conf: JWT_ACCESS_EXPIRE_SECOND 必须大于 0")
	}

	// 生产环境禁止弱密钥，避免把测试配置带上线
	if c.IsProd() {
		if _, weak := weakSecrets[c.App.JWTSecret]; weak || len(c.App.JWTSecret) < 32 {
			return fmt.Errorf("conf: 生产环境 JWT_SECRET 必须是长度不小于 32 的强随机串")
		}
		if c.DB.Password == "" {
			return fmt.Errorf("conf: 生产环境 DB_PASSWORD 不能为空")
		}
	}
	return nil
}
