// Package mq 定义消息投递的抽象与实现。
//
// 为什么要有这层抽象：投递目标是环境相关的——本地开发可能只需要打日志，
// 自建环境用 Kafka，云上可能换成阿里云 Kafka 或 RocketMQ。业务代码只依赖
// Publisher 接口，切换实现只需改一个配置项。
package mq

import "context"

// Publisher 是消息发布者。
//
// 约定：
//   - Publish 返回 nil 才表示「已被下游接收」，任何非 nil 都视为投递失败，
//     由调用方（Outbox Relay）负责重试；
//   - 实现必须保证同 Key 的消息按调用顺序进入同一分区/队列，
//     这是消息顺序性的基础。
type Publisher interface {
	// Publish 发布单条消息。
	// topic 为逻辑主题名，key 为分区键（本项目统一用会话 ID），
	// value 为消息体。
	Publish(ctx context.Context, topic, key string, value []byte) error

	// Close 释放底层资源。
	Close() error
}
