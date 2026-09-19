package mq

import (
	"fmt"

	klog "github.com/go-kratos/kratos/v2/log"

	"github.com/Jesse-467/im/Chat/internal/conf"
)

// 投递实现类型
const (
	// TypeLog 把消息投递到日志，仅用于本地开发与测试
	TypeLog = "log"
	// TypeKafka 投递到 Kafka（含自建与云上兼容 Kafka 协议的产品）
	TypeKafka = "kafka"
)

// NewPublisher 按配置选择投递实现。
//
// 这是"基础设施可切换"的落点：业务代码只依赖 Publisher 接口，
// 更换中间件时改动被收敛在这一个函数里。
func NewPublisher(c *conf.Config, logger klog.Logger) (Publisher, func(), error) {
	switch c.MQ.Type {
	case TypeKafka:
		return NewKafkaPublisher(c, logger)

	case TypeLog, "":
		// 生产环境禁止日志投递：它不提供持久化与重放，消息一旦"投递"即丢失，
		// 而且失败是静默的——这比直接启动失败危险得多。
		if c.IsProd() {
			return nil, nil, fmt.Errorf("mq: 生产环境不允许使用 %q 类型的投递实现，请配置 MQ_TYPE=kafka", TypeLog)
		}
		p := NewLogPublisher(logger)
		return p, func() { _ = p.Close() }, nil

	default:
		return nil, nil, fmt.Errorf("mq: 不支持的 MQ_TYPE=%q，可选 %s | %s", c.MQ.Type, TypeKafka, TypeLog)
	}
}

// NewSubscriber 按配置选择订阅实现。
func NewSubscriber(c *conf.Config, logger klog.Logger) (Subscriber, func(), error) {
	switch c.MQ.Type {
	case TypeKafka:
		return NewKafkaSubscriber(c, logger)

	case TypeLog, "":
		if c.IsProd() {
			return nil, nil, fmt.Errorf("mq: 生产环境不允许使用 %q 类型的订阅实现，请配置 MQ_TYPE=kafka", TypeLog)
		}
		s := NewLogSubscriber(logger)
		return s, func() { _ = s.Close() }, nil

	default:
		return nil, nil, fmt.Errorf("mq: 不支持的 MQ_TYPE=%q，可选 %s | %s", c.MQ.Type, TypeKafka, TypeLog)
	}
}
