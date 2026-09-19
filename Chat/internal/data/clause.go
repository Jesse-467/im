package data

import (
	"gorm.io/gorm/clause"
)

// clauseLockingSkipLocked 构造 FOR UPDATE SKIP LOCKED 子句。
//
// 单独封装的原因：GORM 对「跳过已锁定行」没有开箱支持，需要手写 Expression。
// 把 SQL 细节集中在这里，调用处只表达意图（"取一批且不与别人抢"）。
func clauseLockingSkipLocked() clause.Interface {
	return clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}
}
