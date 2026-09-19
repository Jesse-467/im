package mq

import (
	"context"
	"sync"

	klog "github.com/go-kratos/kratos/v2/log"
)

// 编译期断言：日志发布者必须满足发布者契约。
var _ Publisher = (*LogPublisher)(nil)

// LogPublisher 把消息投递到日志，并可选地直接分发给本地订阅者。
//
// 用途：本地开发与自动化测试。它让「Outbox → 投递 → 消费 → 推送」这条链路
// 可以在没有任何消息中间件的情况下完整跑通并被断言，
// 从而避免把「能不能连上 Kafka」变成验证业务逻辑的前提。
//
// 生产环境不应使用它：日志不提供持久化与重放能力，
// 这一点通过配置校验（生产环境禁止 log 类型）来强制。
type LogPublisher struct {
	log *klog.Helper

	// mu 保护 handler：RegisterHandler 可能由启动协程调用，
	// 而 Publish 由 Relay 协程调用，两者存在并发。
	mu      sync.RWMutex
	handler Handler
}

// NewLogPublisher 构造日志发布者。
func NewLogPublisher(logger klog.Logger) *LogPublisher {
	return &LogPublisher{log: klog.NewHelper(klog.With(logger, "module", "mq/log"))}
}

// RegisterHandler 注册本地分发处理器。
//
// 日志模式没有真实队列，因此由发布者直接把消息交给处理器，
// 使两种模式对上层行为一致：无论走不走真实队列，
// 最终都由同一个消费者完成「查成员 → 推送给在线连接」。
func (p *LogPublisher) RegisterHandler(h Handler) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.handler = h
}

// Publish 打印消息内容，并在注册了处理器时同步分发。
//
// 分发失败不影响返回值：日志模式下没有重试语义，
// 失败由消费者自身降级处理（离线消息靠客户端拉取补齐）。
func (p *LogPublisher) Publish(ctx context.Context, topic, key string, value []byte) error {
	p.log.Infow("msg", "投递消息（日志模式）", "topic", topic, "key", key, "payload", string(value))

	p.mu.RLock()
	h := p.handler
	p.mu.RUnlock()

	if h != nil {
		if err := h(ctx, topic, key, value); err != nil {
			p.log.Warnw("msg", "本地分发失败", "topic", topic, "key", key, "err", err)
		}
	}
	return nil
}

// Close 无资源需要释放。
func (p *LogPublisher) Close() error { return nil }
