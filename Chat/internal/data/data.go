// Package data 实现 biz 层声明的仓储接口，并持有聊天服务的全部基础设施客户端。
//
// 本层是唯一感知具体中间件（PostgreSQL / Redis / 云组件）的地方。业务层只看到接口，
// 因此更换存储实现不会影响业务规则（会话、消息、好友等规则均与存储无关）。
package data

import (
	"fmt"
	"time"

	klog "github.com/go-kratos/kratos/v2/log"
	"github.com/redis/go-redis/v9"
	gormpg "gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/Jesse-467/im/Chat/internal/conf"
)

// Data 持有数据库与缓存客户端，由 Wire 在启动时构建一次，进程内全局复用。
type Data struct {
	db    *gorm.DB
	cache redis.UniversalClient
	log   *klog.Helper
}

// NewData 构建基础设施客户端，并返回释放资源的 cleanup 函数。
//
// 关键设计：此处不会立刻与数据库建立真实连接——GORM 关闭了自动 ping，
// Redis 客户端本身也是惰性连接。因此进程可以在依赖尚未就绪时正常启动，
// 由 /readyz 如实反映依赖健康状态。
//
// 这样做的收益：
//  1. 避免容器编排下「依赖未起 → 进程崩溃 → 重启风暴」的死锁；
//  2. 存活与就绪的语义分离，依赖抖动时不会被误杀，只会被摘流。
func NewData(c *conf.Config, logger klog.Logger) (*Data, func(), error) {
	log := klog.NewHelper(klog.With(logger, "module", "data"))

	db, err := newDB(c)
	if err != nil {
		return nil, nil, err
	}

	cache, err := newCache(c)
	if err != nil {
		return nil, nil, err
	}

	cleanup := func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
		_ = cache.Close()
		log.Info("msg", "基础设施连接已释放")
	}

	return &Data{db: db, cache: cache, log: log}, cleanup, nil
}

// DB 暴露数据库连接，供健康检查等横切组件使用。
//
// 之所以提供导出方法而非导出字段：字段私有可防止业务代码绕过仓储层直接操作连接，
// 而健康检查确实需要拿到原始连接做 Ping，因此只开放只读的访问入口。
func (d *Data) DB() *gorm.DB { return d.db }

// Cache 暴露缓存客户端，供健康检查等横切组件使用。
func (d *Data) Cache() redis.UniversalClient { return d.cache }

// newDB 按 DB_TYPE 构建数据库连接。
//
// postgres / polardb-pg / rds-pg 均兼容 PostgreSQL 协议，共用 pgx 驱动；
// 保留类型分支是为了后续按类型注入不同的连接参数（如云数据库要求的 SSL 模式）。
func newDB(c *conf.Config) (*gorm.DB, error) {
	// 日志级别：生产只记错误，其余环境记慢查询与错误。
	//
	// 刻意不用 Info 级别：它会打印每一条 SQL，而本项目有轮询型后台任务
	// （Outbox Relay 每 300ms 查一次），Info 级别会把日志彻底淹没，
	// 真正的错误反而看不见。需要看 SQL 时用 GORM_DEBUG 单独开启。
	level := gormlogger.Warn
	if c.IsProd() {
		level = gormlogger.Error
	}
	if c.DB.DebugSQL {
		level = gormlogger.Info
	}

	db, err := gorm.Open(gormpg.Open(c.DB.EffectiveDSN()), &gorm.Config{
		// 关键：不在启动时建立真实连接，使进程可以先于数据库启动，
		// 由 /readyz 如实反映依赖状态，避免容器编排下「依赖未起 → 进程退出 → 重启风暴」。
		// pgx 的 sql.Open 本身是惰性的，配合该开关即可实现完全惰性初始化。
		DisableAutomaticPing:   true,
		SkipDefaultTransaction: true, // 单条写入无需隐式事务；跨表一致性由业务显式控制
		Logger:                 gormlogger.Default.LogMode(level),
	})
	if err != nil {
		return nil, fmt.Errorf("data: 初始化数据库失败 (type=%s): %w", c.DB.Type, err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("data: 获取数据库连接池失败: %w", err)
	}
	sqlDB.SetMaxOpenConns(c.DB.MaxOpenConns)
	sqlDB.SetMaxIdleConns(c.DB.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(c.DB.ConnMaxLifetime)

	return db, nil
}

// newCache 按 CACHE_TYPE 构建缓存客户端。
//
// 支持单机、集群、哨兵三种形态；接入 Tair 等云产品时通常兼容 Redis 协议，
// 只需在此新增一个分支即可，业务代码零改动。
func newCache(c *conf.Config) (redis.UniversalClient, error) {
	if len(c.Cache.Addrs) == 0 {
		return nil, fmt.Errorf("data: 缓存地址为空 (CACHE_ADDRS)")
	}

	switch c.Cache.Type {
	case "redis-cluster":
		return redis.NewClusterClient(&redis.ClusterOptions{
			Addrs:    c.Cache.Addrs,
			Password: c.Cache.Password,
			PoolSize: c.Cache.PoolSize,
		}), nil

	case "redis-sentinel":
		if c.Cache.MasterName == "" {
			return nil, fmt.Errorf("data: 哨兵模式必须配置 CACHE_MASTER_NAME")
		}
		return redis.NewFailoverClient(&redis.FailoverOptions{
			MasterName:    c.Cache.MasterName,
			SentinelAddrs: c.Cache.Addrs,
			Password:      c.Cache.Password,
			DB:            c.Cache.DB,
			PoolSize:      c.Cache.PoolSize,
		}), nil

	case "redis-standalone", "tair", "":
		return redis.NewClient(&redis.Options{
			Addr:         c.Cache.Addrs[0],
			Password:     c.Cache.Password,
			DB:           c.Cache.DB,
			PoolSize:     c.Cache.PoolSize,
			DialTimeout:  5 * time.Second,
			ReadTimeout:  3 * time.Second,
			WriteTimeout: 3 * time.Second,
		}), nil

	default:
		return nil, fmt.Errorf("data: 不支持的 CACHE_TYPE=%q", c.Cache.Type)
	}
}
