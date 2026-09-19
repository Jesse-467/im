package mq

import (
	"context"
	"crypto/tls"
	"fmt"
	"time"

	klog "github.com/go-kratos/kratos/v2/log"
	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl/plain"

	"github.com/Jesse-467/im/Chat/internal/conf"
)

// 编译期断言：Kafka 发布者必须满足发布者契约。
var _ Publisher = (*KafkaPublisher)(nil)

// KafkaPublisher 是基于 Kafka 的消息发布者。
type KafkaPublisher struct {
	writer *kafka.Writer
	log    *klog.Helper
}

// NewKafkaPublisher 按配置构造 Kafka 发布者。
//
// 关键参数及其理由：
//   - RequiredAcks = RequireAll：等所有同步副本确认。若用默认的 RequireNone，
//     broker 收到即返回成功，一旦 leader 在复制前宕机消息就永久丢失，
//     而此时 Outbox 已被标记为"已投递"，重试也无从谈起；
//   - MaxAttempts = 3：网络抖动由客户端内部重试，避免把可恢复的瞬时故障
//     升级为需要 Outbox 重试的持久失败；
//   - BatchTimeout 很短：即时通讯对延迟敏感，攒批时间长会直接体现为消息延迟。
func NewKafkaPublisher(c *conf.Config, logger klog.Logger) (*KafkaPublisher, func(), error) {
	if len(c.MQ.Brokers) == 0 {
		return nil, nil, fmt.Errorf("mq: MQ_BROKERS 不能为空")
	}

	transport := &kafka.Transport{
		// 与 Outbox 的写入超时保持同一量级，避免请求悬挂过久
		DialTimeout: 5 * time.Second,
	}
	if c.MQ.Username != "" {
		transport.SASL = plain.Mechanism{Username: c.MQ.Username, Password: c.MQ.Password}
	}
	// 云 Kafka（如阿里云）通常要求 TLS；本地明文则不需要
	if c.MQ.TLS {
		transport.TLS = &tls.Config{MinVersion: tls.VersionTLS12}
	}

	w := &kafka.Writer{
		Addr:         kafka.TCP(c.MQ.Brokers...),
		Transport:    transport,
		Balancer:     &kafka.Hash{}, // 按 key 哈希分区：同会话消息进同一分区，从而有序
		RequiredAcks: kafka.RequireAll,
		MaxAttempts:  3,
		BatchTimeout: 20 * time.Millisecond,
		BatchSize:    64,
		Async:        false, // 同步写，确保返回 nil 时消息确已被 broker 接收
		Compression:  kafka.Snappy,
	}

	p := &KafkaPublisher{
		writer: w,
		log:    klog.NewHelper(klog.With(logger, "module", "mq/kafka")),
	}

	cleanup := func() {
		if err := w.Close(); err != nil {
			p.log.Errorw("msg", "关闭 Kafka 写入器失败", "err", err)
		}
	}

	p.log.Infow("msg", "Kafka 发布者已就绪", "brokers", c.MQ.Brokers)
	return p, cleanup, nil
}

// Publish 发布一条消息。
func (p *KafkaPublisher) Publish(ctx context.Context, topic, key string, value []byte) error {
	err := p.writer.WriteMessages(ctx, kafka.Message{
		Topic: topic,
		Key:   []byte(key),
		Value: value,
		Time:  time.Now(),
	})
	if err != nil {
		return fmt.Errorf("mq: 投递到 %s（key=%s）失败: %w", topic, key, err)
	}
	return nil
}

// Close 释放写入器。
func (p *KafkaPublisher) Close() error { return p.writer.Close() }
