package data

import (
	"github.com/google/wire"
	"github.com/redis/go-redis/v9"

	"github.com/Jesse-467/im/Chat/internal/health"
)

// ProviderSet 是 data 层的依赖注入集合。
//
// 所有仓储构造函数都返回 biz 层声明的接口类型，从而让 Wire 完成
// 「实现 → 接口」的绑定，业务层因此只依赖接口。
var ProviderSet = wire.NewSet(
	NewData,
	ProvideCache,
	NewConversationRepo,
	NewFriendRepo,
	NewMessageRepo,
	NewOutboxRepo,
	NewSeqAllocator,
	NewSnowflake,
	NewIDGenerator,
	NewNodeID,
	NewAccountClient,
	NewPostgresChecker,
	NewRedisChecker,
	NewHealthCheckers,
)

// ProvideCache 从 Data 中暴露出缓存客户端。
//
// 为什么要单独暴露：WebSocket 网关的在线状态（Presence）需要直接操作 Redis，
// 但它是「连接层」能力而非「仓储」，不该被打包成 biz 接口。
// 由这里统一提供客户端实例，保证全进程复用同一个连接池。
func ProvideCache(d *Data) redis.UniversalClient {
	return d.cache
}

// NewHealthCheckers 汇总全部依赖探测器，供 /readyz 使用。
//
// 新增依赖（如消息队列）时只需在此追加，探针会自动纳入。
func NewHealthCheckers(postgres *PostgresChecker, redis *RedisChecker) []health.Checker {
	return []health.Checker{postgres, redis}
}
