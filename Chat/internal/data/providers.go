package data

import (
	"github.com/google/wire"

	"github.com/Jesse-467/im/Chat/internal/health"
)

// ProviderSet 是 data 层的依赖注入集合。
//
// 仓储构造函数的返回类型刻意声明为 biz 层声明的接口：Wire 依据返回类型把实现
// 绑定到契约上，biz 因此只依赖接口、不 import 本包，更换存储实现无需触碰业务代码。
var ProviderSet = wire.NewSet(
	NewData,
	NewIDGenerator,
	NewConversationRepo,
	NewFriendRepo,
	NewMessageRepo,
	NewAccountClient,
	NewPostgresChecker,
	NewRedisChecker,
	NewHealthCheckers,
)

// NewHealthCheckers 汇总全部依赖探测器，供 /readyz 使用。
//
// 新增依赖（如 Kafka、etcd）时只需在此追加，探针会自动纳入。
func NewHealthCheckers(postgres *PostgresChecker, redis *RedisChecker) []health.Checker {
	return []health.Checker{postgres, redis}
}
