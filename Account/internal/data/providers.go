package data

import (
	"github.com/google/wire"

	"github.com/Jesse-467/im/Account/internal/health"
)

// ProviderSet 是 data 层的依赖注入集合。
var ProviderSet = wire.NewSet(
	NewData,
	NewUserRepo,
	NewMySQLChecker,
	NewRedisChecker,
	NewHealthCheckers,
)

// NewHealthCheckers 汇总全部依赖探测器，供 /readyz 使用。
//
// 新增依赖（如 Kafka、etcd）时只需在此追加，探针会自动纳入。
func NewHealthCheckers(mysql *MySQLChecker, redis *RedisChecker) []health.Checker {
	return []health.Checker{mysql, redis}
}
