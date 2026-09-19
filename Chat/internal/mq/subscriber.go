package mq

import "context"

// Handler 处理一条消费到的消息。
//
// 返回 error 表示处理失败，由消费实现决定是重试还是跳过。
// 实现方必须保证幂等：投递语义是 at-least-once，
// 同一条消息可能因为重试或消费者重启而被重复投递。
type Handler func(ctx context.Context, topic, key string, value []byte) error

// Subscriber 是消息订阅者。
type Subscriber interface {
	// Subscribe 订阅指定主题并阻塞消费，直到 ctx 被取消。
	//
	// group 是消费者组标识。本项目的约定是「每实例独立组」：
	// 每个网关实例都需要收到全部消息，才能把消息推给本节点上的在线用户。
	// 若共用同一个组，消息会被实例瓜分，导致部分用户永远收不到推送。
	Subscribe(ctx context.Context, topic, group string, handler Handler) error

	// Close 释放底层资源。
	Close() error
}
