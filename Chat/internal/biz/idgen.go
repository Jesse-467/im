package biz

// IDGenerator 生成全局唯一、趋势递增的 int64 ID。
//
// 为什么放在 biz 而不是让 data 层在插入时顺手生成：
// 会话 ID 在这里不只是主键——群聊要用它拼业务去重键，消息要用它做幂等键，
// 投递时还要拿它当消息队列的分区键。这些都发生在"插入之前"，
// 因此 ID 必须是业务层可以提前拿到并参与决策的值，而不是插入的副产物。
type IDGenerator interface {
	Next() (int64, error)
}
