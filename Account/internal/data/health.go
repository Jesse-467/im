package data

import (
	"context"

	"github.com/Jesse-467/im/Account/internal/health"
)

// 编译期断言：两个探测器都必须满足健康检查契约。
var (
	_ health.Checker = (*MySQLChecker)(nil)
	_ health.Checker = (*RedisChecker)(nil)
)

// MySQLChecker 探测数据库可用性。
type MySQLChecker struct{ data *Data }

// NewMySQLChecker 构造数据库探测器。
func NewMySQLChecker(d *Data) *MySQLChecker { return &MySQLChecker{data: d} }

// Name 返回依赖名称。
func (c *MySQLChecker) Name() string { return "mysql" }

// Check 通过连接池 Ping 判断数据库是否可用。
func (c *MySQLChecker) Check(ctx context.Context) error {
	sqlDB, err := c.data.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.PingContext(ctx)
}

// RedisChecker 探测缓存可用性。
type RedisChecker struct{ data *Data }

// NewRedisChecker 构造缓存探测器。
func NewRedisChecker(d *Data) *RedisChecker { return &RedisChecker{data: d} }

// Name 返回依赖名称。
func (c *RedisChecker) Name() string { return "redis" }

// Check 通过 PING 判断缓存是否可用。
func (c *RedisChecker) Check(ctx context.Context) error {
	return c.data.cache.Ping(ctx).Err()
}
