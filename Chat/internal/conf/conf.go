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
	ServiceName string // chat

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
// Type 取值：mysql（本地）| polardb | rds。
// 当前三种 Type 共用 MySQL 协议，保留字段是为了后续按类型注入不同的连接参数与降级策略。
type DB struct {
	Type            string
	Host            string
	Port            int
	User            string
	Password        string
	Name            string
	DSN             string // 显式指定时优先级最高
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

// EffectiveDSN 返回最终使用的 DSN：显式配置优先，否则按分项拼接。
func (d DB) EffectiveDSN() string {
	if d.DSN != "" {
		return d.DSN
	}
	return fmt.Sprintf(
		"%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=true&loc=Local&timeout=5s&readTimeout=10s&writeTimeout=10s",
		d.User, d.Password, d.Host, d.Port, d.Name,
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
	GRPCAddr        string
	JWTSecret       string
	JWTAccessExpire time.Duration
	// AccountRPCEndpoint 是 Chat 依赖的账号中心 gRPC 地址。
	//
	// 聊天服务只通过 gRPC 契约消费账号能力（如用户信息查询），
	// 因此这里保存的是「下游依赖地址」而非本服务的监听地址。
	AccountRPCEndpoint string
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
			Type:            envString("DB_TYPE", "mysql"),
			Host:            envString("DB_HOST", "127.0.0.1"),
			Port:            envInt("DB_PORT", 3306),
			User:            envString("DB_USER", "root"),
			Password:        envString("DB_PASSWORD", ""),
			Name:            envString("DB_NAME", "im_chat"),
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
			TopicMessage:  envString("MQ_TOPIC_MESSAGE", "message"),
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
			HTTPAddr:           envString("HTTP_ADDR", "0.0.0.0:8002"),
			GRPCAddr:           envString("GRPC_ADDR", "0.0.0.0:9002"),
			JWTSecret:          envString("JWT_SECRET", ""),
			JWTAccessExpire:    envDuration("JWT_ACCESS_EXPIRE", 86400*time.Second),
			AccountRPCEndpoint: envString("ACCOUNT_RPC_ENDPOINT", "127.0.0.1:9001"),
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
	// 账号中心是 Chat 的强依赖（身份信息、好友/资料等能力都要经 gRPC 取用），
	// 缺失时直接拒绝启动，避免运行时才暴露「调用地址为空」的低级故障。
	if c.App.AccountRPCEndpoint == "" {
		return fmt.Errorf("conf: ACCOUNT_RPC_ENDPOINT 不能为空")
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
