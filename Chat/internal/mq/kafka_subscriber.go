package mq

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"time"

	klog "github.com/go-kratos/kratos/v2/log"
	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl/plain"

	"github.com/Jesse-467/im/Chat/internal/conf"
)

// 消费参数
const (
	// consumeCommitInterval 是自动提交 offset 的间隔。
	//
	// 为什么不每条都提交：频繁提交会显著增加 broker 压力，
	// 而本项目允许少量重复（消费端按 (conversationId, seq) 幂等去重），
	// 因此用较短间隔提交即可在「重复量」与「开销」之间取得平衡。
	consumeCommitInterval = time.Second
	// consumeMaxBytes 单次拉取的最大字节数
	consumeMaxBytes = 10 << 20 // 10MB
)

// 编译期断言：Kafka 订阅者必须满足订阅者契约。
var _ Subscriber = (*KafkaSubscriber)(nil)

// KafkaSubscriber 是基于 Kafka 的消息订阅者。
type KafkaSubscriber struct {
	cfg *conf.Config
	log *klog.Helper

	reader *kafka.Reader
}

// NewKafkaSubscriber 按配置构造 Kafka 订阅者。
func NewKafkaSubscriber(c *conf.Config, logger klog.Logger) (*KafkaSubscriber, func(), error) {
	s := &KafkaSubscriber{
		cfg: c,
		log: klog.NewHelper(klog.With(logger, "module", "mq/kafka-sub")),
	}
	// reader 在 Subscribe 时按具体 topic 构造（一个订阅者可能订阅多个主题），
	// 因此这里的 cleanup 只负责关闭已创建的 reader。
	cleanup := func() {
		_ = s.Close()
	}
	return s, cleanup, nil
}

// Subscribe 订阅主题并阻塞消费。
func (s *KafkaSubscriber) Subscribe(ctx context.Context, topic, group string, handler Handler) error {
	transport := &kafka.Transport{DialTimeout: 5 * time.Second}
	if s.cfg.MQ.Username != "" {
		transport.SASL = plain.Mechanism{Username: s.cfg.MQ.Username, Password: s.cfg.MQ.Password}
	}
	if s.cfg.MQ.TLS {
		transport.TLS = &tls.Config{MinVersion: tls.VersionTLS12}
	}

	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers: s.cfg.MQ.Brokers,
		Topic:   topic,
		// GroupID 取「每实例独立组」：所有网关实例都必须收到全量消息，
		// 才能各自推给本节点上的在线用户。若共用组，消息会被瓜分，
		// 表现为「部分用户永远收不到推送」。
		GroupID: group,
		// 未指定分区：交由 Kafka 做消费者组内的分区分配
		MinBytes:       1,
		MaxBytes:       consumeMaxBytes,
		MaxWait:        200 * time.Millisecond,
		CommitInterval: consumeCommitInterval,
		// 从最早未提交位置开始：消费者重启后不会丢掉停机期间的消息，
		// 这些消息会在恢复后补推（客户端另有 seq 补洞兜底）。
		StartOffset: kafka.FirstOffset,
		Dialer:      &kafka.Dialer{Timeout: 5 * time.Second, DualStack: true},
	})

	s.reader = reader
	s.log.Infow("msg", "Kafka 订阅已启动", "topic", topic, "group", group, "brokers", s.cfg.MQ.Brokers)

	defer func() {
		if err := reader.Close(); err != nil {
			s.log.Errorw("msg", "关闭 Kafka 读取器失败", "err", err)
		}
	}()

	for {
		msg, err := reader.FetchMessage(ctx)
		if err != nil {
			// ctx 取消是正常的退出路径，不作为错误上报
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				s.log.Infow("msg", "Kafka 订阅已停止", "topic", topic)
				return nil
			}
			// 其他错误（如 broker 短暂不可用）：退避后继续，
			// 而不是直接退出——否则一次网络抖动就会让推送链路彻底停摆。
			s.log.Errorw("msg", "拉取消息失败，稍后重试", "topic", topic, "err", err)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(time.Second):
				continue
			}
		}

		if err := handler(ctx, msg.Topic, string(msg.Key), msg.Value); err != nil {
			// 处理失败仍提交 offset。
			//
			// 这是刻意的取舍：本项目的推送失败原因通常是「用户不在线」
			// 或「连接已满」，重试没有意义——离线消息由客户端通过 seq 拉取补齐，
			// 属于另一条更可靠的补偿路径。若在这里无限重试，
			// 一条坏消息会阻塞整个分区，影响该分区上所有会话的推送。
			s.log.Warnw("msg", "处理消息失败，跳过并提交",
				"topic", msg.Topic, "key", string(msg.Key), "err", err)
		}

		if err := reader.CommitMessages(ctx, msg); err != nil {
			s.log.Errorw("msg", "提交 offset 失败", "topic", msg.Topic, "err", err)
		}
	}
}

// Close 释放读取器。
func (s *KafkaSubscriber) Close() error {
	if s.reader == nil {
		return nil
	}
	return s.reader.Close()
}

// 用于触发 fmt 包引用
var _ = fmt.Sprintf
