package data

import (
	"github.com/google/wire"

	"github.com/Jesse-467/im/Chat/internal/health"
)

// ProviderSet 是 data 层的依赖注入集合。
//
// 当前骨架阶段只装配基础设施（DB / 缓存）与健康检查；
// 会话、消息、好友等仓储实现将在业务层落地时追加到此处。
var ProviderSet = wire.NewSet(
	NewData,
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
