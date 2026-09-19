package mq

import (
	"context"

	klog "github.com/go-kratos/kratos/v2/log"
)

// 编译期断言：日志订阅者必须满足订阅者契约。
var _ Subscriber = (*LogSubscriber)(nil)

// LogSubscriber 是把消息处理结果打日志的订阅者。
//
// 用途：本地开发与自动化测试。它让完整的「发送 → Outbox → 投递 → 消费 → 推送」
// 链路可以在没有任何消息中间件的情况下跑通并被断言。
//
// 生产环境不应使用：它不会真正消费队列，消息不会被确认，
// 实际上等于把推送链路断开。
type LogSubscriber struct {
	log *klog.Helper
}

// NewLogSubscriber 构造日志订阅者。
func NewLogSubscriber(logger klog.Logger) *LogSubscriber {
	return &LogSubscriber{log: klog.NewHelper(klog.With(logger, "module", "mq/log-sub"))}
}

// Subscribe 阻塞直到 ctx 取消。
//
// 刻意不模拟"消费到消息"：本地模式下消息由投递方直接调用处理器分发
// （见 DispatchPublisher），若这里也消费会造成重复推送。
func (s *LogSubscriber) Subscribe(ctx context.Context, topic, group string, _ Handler) error {
	s.log.Infow("msg", "订阅已启动（日志模式，不实际消费）", "topic", topic, "group", group)
	<-ctx.Done()
	s.log.Infow("msg", "订阅已停止", "topic", topic, "group", group)
	return nil
}

// Close 无资源需要释放。
func (s *LogSubscriber) Close() error { return nil }
