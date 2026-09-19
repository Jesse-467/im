package conf

import (
	"strings"
	"testing"
	"time"
)

// validConfig 返回一份可通过校验的基线配置。
//
// 每个用例只改动自己关心的字段，其余保持合法——这样断言失败时
// 原因一定在改动的那一项上，而不是被其他缺失项干扰。
func validConfig() *Config {
	return &Config{
		Environment: EnvDev,
		ServiceName: "chat",
		DB: DB{
			Type:         "postgres",
			Host:         "127.0.0.1",
			Port:         5432,
			User:         "postgres",
			Password:     "im_local_pass",
			Name:         "im_chat",
			SSLMode:      "disable",
			MaxOpenConns: 100,
		},
		Cache: Cache{
			Type:     "redis-standalone",
			Addrs:    []string{"127.0.0.1:6379"},
			PoolSize: 100,
		},
		MQ: MQ{
			Type:         "kafka",
			Brokers:      []string{"127.0.0.1:19092"},
			TopicPrefix:  "im",
			TopicMessage: "message",
		},
		App: App{
			HTTPAddr:           "0.0.0.0:8002",
			JWTSecret:          "dev_only_secret_please_change_in_production",
			JWTAccessExpire:    24 * time.Hour,
			AccountRPCEndpoint: "127.0.0.1:9001",
		},
	}
}

// TestValidateAcceptsBaseline 确认基线配置本身是合法的。
//
// 若这条失败，说明基线写错了，后续用例的失败原因都不可信。
func TestValidateAcceptsBaseline(t *testing.T) {
	if err := validConfig().Validate(); err != nil {
		t.Fatalf("基线配置应当通过校验: %v", err)
	}
}

// TestEffectiveDSN 验证 DSN 的生成与显式覆盖。
func TestEffectiveDSN(t *testing.T) {
	d := validConfig().DB
	got := d.EffectiveDSN()
	for _, want := range []string{"host=127.0.0.1", "port=5432", "dbname=im_chat", "sslmode=disable"} {
		if !strings.Contains(got, want) {
			t.Errorf("DSN 缺少 %q: %s", want, got)
		}
	}

	// 显式 DSN 优先级最高：云端常把完整连接串直接注入环境变量，
	// 此时分散的 host/port 等字段应被完全忽略，避免拼出错误的连接串。
	d.DSN = "postgres://user:pass@cloud-host:5432/im_chat"
	if got := d.EffectiveDSN(); got != d.DSN {
		t.Errorf("显式 DSN 未被优先使用: %s", got)
	}
}

// TestValidateRejectsIllegalEnvironment 验证运行环境取值受限。
//
// Environment 决定生产环境的强化校验是否生效，取值写错会让服务
// 在「以为是测试」的假设下带着弱密钥上线，因此必须拒绝。
func TestValidateRejectsIllegalEnvironment(t *testing.T) {
	for _, env := range []string{"", "production", "PROD", "staging", "local"} {
		c := validConfig()
		c.Environment = env
		if err := c.Validate(); err == nil {
			t.Errorf("Environment=%q 应当被拒绝", env)
		}
	}
}

// TestValidateAcceptsAllEnvironments 验证三个合法环境都能通过。
func TestValidateAcceptsAllEnvironments(t *testing.T) {
	for _, env := range []string{EnvDev, EnvTest, EnvProd} {
		c := validConfig()
		c.Environment = env
		if env == EnvProd {
			// 生产环境要求更强的密钥，否则会命中弱密钥校验
			c.App.JWTSecret = "a_very_long_and_random_secret_for_prod_use_32+"
		}
		if err := c.Validate(); err != nil {
			t.Errorf("Environment=%q 应当被接受: %v", env, err)
		}
	}
}

// TestValidateRequiredFields 验证关键字段缺失时启动失败。
//
// 每个字段对应一类「启动时能拦住、运行时很难查」的故障：
// 缺 DB 会在第一次请求时报错，缺 ACCOUNT_RPC_ENDPOINT 会在首次调用账号中心时才发现。
func TestValidateRequiredFields(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"空 ServiceName", func(c *Config) { c.ServiceName = "" }},
		{"无数据库配置", func(c *Config) { c.DB = DB{MaxOpenConns: 100} }},
		{"连接池上限为 0", func(c *Config) { c.DB.MaxOpenConns = 0 }},
		{"负数连接池上限", func(c *Config) { c.DB.MaxOpenConns = -1 }},
		{"空 HTTP 地址", func(c *Config) { c.App.HTTPAddr = "" }},
		{"空 JWT 密钥", func(c *Config) { c.App.JWTSecret = "" }},
		{"令牌有效期为 0", func(c *Config) { c.App.JWTAccessExpire = 0 }},
		{"负数令牌有效期", func(c *Config) { c.App.JWTAccessExpire = -time.Hour }},
		{"无账号中心地址", func(c *Config) { c.App.AccountRPCEndpoint = "" }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validConfig()
			tc.mutate(c)
			if err := c.Validate(); err == nil {
				t.Fatalf("配置 %s 应当被拒绝", tc.name)
			}
		})
	}
}

// TestFatalIssuesForRequiredComponents 验证必需组件缺失判定为致命。
//
// 这些组件缺失后服务无法对外提供任何有意义的服务，必须在启动时退出，
// 而不是等第一个请求进来才失败。
func TestFatalIssuesForRequiredComponents(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"无数据库配置", func(c *Config) { c.DB = DB{MaxOpenConns: 100} }},
		{"无账号中心地址", func(c *Config) { c.App.AccountRPCEndpoint = "" }},
		{"空 HTTP 地址", func(c *Config) { c.App.HTTPAddr = "" }},
		{"空 JWT 密钥", func(c *Config) { c.App.JWTSecret = "" }},
		{"Environment 非法", func(c *Config) { c.Environment = "staging" }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validConfig()
			tc.mutate(c)
			fatal := c.FatalIssues()
			if len(fatal) == 0 {
				t.Fatalf("配置 %s 应产生致命问题", tc.name)
			}
		})
	}
}

// TestOptionalComponentsOnlyWarn 验证非必要组件缺失不阻断启动。
//
// 这是可用性与正确性之间的取舍：缓存与消息队列不可用时服务仍能接受请求，
// 只是推送链路断开、序号分配不可用。若把它们也设为硬依赖，
// 任何一个非关键组件抖动都会演变成整个服务不可用。
func TestOptionalComponentsOnlyWarn(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"无缓存地址", func(c *Config) { c.Cache.Addrs = nil }},
		{"开发环境用 log 投递", func(c *Config) { c.MQ.Type = TypeLogFallback }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validConfig()
			tc.mutate(c)

			if fatal := c.FatalIssues(); len(fatal) != 0 {
				t.Fatalf("配置 %s 不应产生致命问题，得到: %+v", tc.name, fatal)
			}

			// 但必须产生告警，否则配置疏漏会被彻底静默
			var warned bool
			for _, i := range c.Check() {
				if !i.IsFatal() {
					warned = true
				}
			}
			if !warned {
				t.Fatalf("配置 %s 应当产生告警", tc.name)
			}
		})
	}
}

// TestCheckReportsAllIssuesAtOnce 验证一次列出全部问题。
//
// 逐项检查最怕「改一个、重启一次、又报下一个」，
// 一次性列全可以让部署配置一次改对。
func TestCheckReportsAllIssuesAtOnce(t *testing.T) {
	c := validConfig()
	c.DB = DB{MaxOpenConns: 100}  // 缺数据库
	c.Cache.Addrs = nil           // 缺缓存
	c.App.AccountRPCEndpoint = "" // 缺账号中心

	issues := c.Check()
	if len(issues) < 3 {
		t.Fatalf("应一次列出至少 3 个问题，实际 %d 个: %+v", len(issues), issues)
	}
}

// TestProdRejectsWeakSecrets 验证生产环境拒绝弱密钥。
//
// 弱密钥一旦上线，任何人都能伪造令牌冒充任意用户，
// 属于最严重的安全问题，必须在启动时拦住而不是靠人工检查。
func TestProdRejectsWeakSecrets(t *testing.T) {
	for _, secret := range []string{
		"change_me",
		"change_me_in_production",
		"secret",
		"123456",
		"short",                           // 长度不足
		"0123456789012345678901234567890", // 31 位，差一位
	} {
		c := validConfig()
		c.Environment = EnvProd
		c.App.JWTSecret = secret
		if err := c.Validate(); err == nil {
			t.Errorf("生产环境应当拒绝弱密钥 %q", secret)
		}
	}
}

// TestProdAcceptsStrongSecret 验证生产环境接受足够强的密钥。
//
// 边界值必须放行，否则会逼运维使用更长的密钥来"绕过"限制。
func TestProdAcceptsStrongSecret(t *testing.T) {
	c := validConfig()
	c.Environment = EnvProd
	c.App.JWTSecret = "01234567890123456789012345678901" // 恰好 32 位
	if err := c.Validate(); err != nil {
		t.Fatalf("长度 32 的密钥应当被接受: %v", err)
	}
}

// TestProdRequiresDBPassword 验证生产环境必须配置数据库密码。
func TestProdRequiresDBPassword(t *testing.T) {
	c := validConfig()
	c.Environment = EnvProd
	c.App.JWTSecret = "01234567890123456789012345678901"
	c.DB.Password = ""
	if err := c.Validate(); err == nil {
		t.Fatal("生产环境缺少数据库密码应当被拒绝")
	}
}

// TestDevAllowsWeakSecret 验证开发环境不强制强密钥。
//
// 本地开发需要用固定密钥复现问题、手写令牌调试，
// 若这里也强制强随机串，会显著抬高开发成本而没有实际收益。
func TestDevAllowsWeakSecret(t *testing.T) {
	c := validConfig()
	c.App.JWTSecret = "dev"
	if err := c.Validate(); err != nil {
		t.Fatalf("开发环境不应限制密钥强度: %v", err)
	}
}

// TestIsProd 验证环境判定的边界。
//
// IsProd 控制着多项安全强校验（弱密钥、空密码），判定出错会静默放宽策略。
func TestIsProd(t *testing.T) {
	c := &Config{}
	c.Environment = EnvProd
	if !c.IsProd() {
		t.Error("prod 应判定为生产环境")
	}
	for _, env := range []string{EnvDev, EnvTest, "PROD", "", "production"} {
		c.Environment = env
		if c.IsProd() {
			t.Errorf("%q 不应判定为生产环境", env)
		}
	}
}

// TestMessageTopic 验证消息主题名的拼接。
//
// 主题名必须带环境前缀：多环境共用同一 Kafka 集群时，
// 若主题名相同，测试环境的消息会被生产消费者读到，造成数据污染。
func TestMessageTopic(t *testing.T) {
	m := MQ{TopicPrefix: "im", TopicMessage: "message"}
	if got := m.MessageTopic(); got != "im.message" {
		t.Fatalf("MessageTopic() = %q, 期望 im.message", got)
	}
}
