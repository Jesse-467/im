package mq

import (
	"context"

	klog "github.com/go-kratos/kratos/v2/log"
)

// 编译期断言：日志发布者必须满足发布者契约。
var _ Publisher = (*LogPublisher)(nil)

// LogPublisher 把消息投递到日志。
//
// 用途：本地开发与自动化测试。它让「Outbox → 投递 → 状态流转」这条链路
// 可以在没有任何消息中间件的情况下完整跑通并被断言，
// 从而避免把「能不能连上 Kafka」变成验证业务逻辑的前提。
//
// 生产环境不应使用它：日志不提供持久化与重放能力，
// 这一点通过配置校验（prod 下禁止 log 类型）来强制。
type LogPublisher struct {
	log *klog.Helper
}

// NewLogPublisher 构造日志发布者。
func NewLogPublisher(logger klog.Logger) *LogPublisher {
	return &LogPublisher{log: klog.NewHelper(klog.With(logger, "module", "mq/log"))}
}

// Publish 打印消息内容。永不失败，因此 Outbox 事件会被正常标记为已投递。
func (p *LogPublisher) Publish(_ context.Context, topic, key string, value []byte) error {
	p.log.Infow("msg", "投递消息（日志模式）", "topic", topic, "key", key, "payload", string(value))
	return nil
}

// Close 无资源需要释放。
func (p *LogPublisher) Close() error { return nil }
