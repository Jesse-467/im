package consumer

import (
	"context"

	"github.com/google/wire"

	"github.com/Jesse-467/im/Chat/internal/biz"
)

// ProviderSet 是消息消费者的依赖注入集合。
//
// 刻意不含 New：消费者需要把 ws.Registry 收窄为 Pusher 接口，
// 这一收窄动作放在 cmd 层的组装函数里完成，因此消费者由那里构造。
var ProviderSet = wire.NewSet(
	ProvideEventLoader,
)

// ProvideEventLoader 把消息仓储适配为消费者的装载接口。
//
// 放在 consumer 包而非 data 包：data 包已依赖 biz 与 ws，
// 若再依赖 consumer 会形成循环。由 consumer 反向适配则依赖方向单向，
// 且「用哪个最小接口」这一决策本就属于消费方。
func ProvideEventLoader(repo biz.MessageRepo) EventLoader {
	return &messageLoader{repo: repo}
}

// messageLoader 基于消息仓储实现装载。
type messageLoader struct {
	repo biz.MessageRepo
}

// LoadMessage 按会话与序号取出一条完整消息。
//
// 复用 ListBySeqRange 而不是让仓储再加一个方法：该查询已经能精确命中
// (conversation_id, seq) 唯一索引，新增一个语义近似的方法只会让仓储接口膨胀。
func (l *messageLoader) LoadMessage(ctx context.Context, conversationID, seq int64) (*biz.Message, error) {
	// fromSeq 传 seq-1 表示开区间下界，limit=1 且升序，恰好取到目标那一条
	msgs, err := l.repo.ListBySeqRange(ctx, conversationID, seq-1, seq, 1, true)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, nil
	}
	return msgs[0], nil
}
