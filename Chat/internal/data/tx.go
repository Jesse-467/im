package data

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// txKey 是事务句柄在 context 中的键。
//
// 用私有类型作键可以彻底避免与其他包放入 context 的键冲突，
// 这是 Go 中传递可选依赖的通行做法。
type txKey struct{}

// WithTx 把事务句柄放入 context。
func WithTx(ctx context.Context, tx *gorm.DB) context.Context {
	return context.WithValue(ctx, txKey{}, tx)
}

// TxFromContext 取出 context 中的事务句柄。
//
// 第二个返回值表示是否存在事务。仓储实现据此决定：有事务就复用，
// 没有就退回普通连接——这样同一份仓储代码既能被独立调用，
// 也能被上层的大事务编排进去，而不用维护两套方法。
func TxFromContext(ctx context.Context) (*gorm.DB, bool) {
	tx, ok := ctx.Value(txKey{}).(*gorm.DB)
	return tx, ok
}

// conn 返回当前应当使用的数据库句柄：优先事务，否则用默认连接。
func (d *Data) conn(ctx context.Context) *gorm.DB {
	if tx, ok := TxFromContext(ctx); ok {
		return tx.WithContext(ctx)
	}
	return d.db.WithContext(ctx)
}

// messageOutboxModel 是 message_outbox 表的 ORM 映射。
type messageOutboxModel struct {
	ID           int64     `gorm:"column:id;primaryKey;autoIncrement"`
	EventID      string    `gorm:"column:event_id;size:64;not null"`
	Topic        string    `gorm:"column:topic;size:64;not null"`
	PartitionKey string    `gorm:"column:partition_key;size:64;not null"`
	Payload      []byte    `gorm:"column:payload;not null"`
	Status       int16     `gorm:"column:status;not null;default:0"`
	RetryCount   int       `gorm:"column:retry_count;not null;default:0"`
	LastError    string    `gorm:"column:last_error;size:255;not null;default:''"`
	NextRetryAt  time.Time `gorm:"column:next_retry_at;not null"`
	CreatedAt    time.Time `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt    time.Time `gorm:"column:updated_at;autoUpdateTime"`
}

// TableName 显式指定表名。
func (messageOutboxModel) TableName() string { return "message_outbox" }
