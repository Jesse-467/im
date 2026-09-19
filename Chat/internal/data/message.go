package data

import (
	"context"
	"fmt"

	"github.com/Jesse-467/im/Chat/internal/biz"
)

// 编译期断言：仓储实现必须满足业务层声明的接口。
var _ biz.MessageRepo = (*messageRepo)(nil)

// messageRepo 是 biz.MessageRepo 的 GORM 实现。
type messageRepo struct{ data *Data }

// NewMessageRepo 构造消息仓储。返回接口类型，便于 Wire 完成依赖绑定。
func NewMessageRepo(d *Data) biz.MessageRepo { return &messageRepo{data: d} }

// FindLastByConversations 批量取每个会话的最后一条消息。
//
// 用 PostgreSQL 的 DISTINCT ON (conversation_id) 配合 ORDER BY conversation_id, seq DESC：
// 数据库在一次索引扫描内就能取出每个会话 seq 最大的那行，返回 map 供会话列表直接查表。
// 换成「先按会话分组求 max(seq) 再回表」需要两次 IO；循环单查则是典型的 N+1，
// 会话数越多越慢，而会话列表恰恰是最高频的接口。
func (r *messageRepo) FindLastByConversations(ctx context.Context, conversationIDs []int64) (map[int64]*biz.Message, error) {
	lasts := make(map[int64]*biz.Message, len(conversationIDs))
	if len(conversationIDs) == 0 {
		return lasts, nil
	}

	var rows []messageModel
	err := r.data.db.WithContext(ctx).Raw(`
		SELECT DISTINCT ON (conversation_id) *
		FROM message
		WHERE conversation_id IN ?
		ORDER BY conversation_id, seq DESC`, conversationIDs).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("data: 批量查询会话最后一条消息失败: %w", err)
	}

	for i := range rows {
		lasts[rows[i].ConversationID] = toBizMessage(&rows[i])
	}
	return lasts, nil
}
