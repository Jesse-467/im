package data

import (
	"github.com/google/wire"

	"github.com/Jesse-467/im/Chat/internal/health"
)

// ProviderSet 是 data 层的依赖注入集合。
//
// 所有仓储构造函数都返回 biz 层声明的接口类型，从而让 Wire 完成
// 「实现 → 接口」的绑定，业务层因此只依赖接口。
var ProviderSet = wire.NewSet(
	NewData,
	NewConversationRepo,
	NewFriendRepo,
	NewMessageRepo,
	NewOutboxRepo,
	NewSeqAllocator,
	NewIDGenerator,
	NewAccountClient,
	NewPostgresChecker,
	NewRedisChecker,
	NewHealthCheckers,
)

// NewHealthCheckers 汇总全部依赖探测器，供 /readyz 使用。
//
// 新增依赖（如消息队列）时只需在此追加，探针会自动纳入。
func NewHealthCheckers(postgres *PostgresChecker, redis *RedisChecker) []health.Checker {
	return []health.Checker{postgres, redis}
}
