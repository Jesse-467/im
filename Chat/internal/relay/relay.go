// Package relay 实现 Outbox 事件的投递协程。
//
// 它是「消息落库」与「消息进入消息队列」之间的桥梁，负责把已经落库但
// 尚未投递的事件搬到队列上。核心要求是 at-least-once：
// 宁可重复投递（消费端可幂等去重），也不能丢失。
package relay

import (
	"context"
	"time"

	klog "github.com/go-kratos/kratos/v2/log"

	"github.com/Jesse-467/im/Chat/internal/biz"
	"github.com/Jesse-467/im/Chat/internal/mq"
)

// 投递参数
const (
	// batchSize 单批处理条数。批量太小会让投递吞吐受限于轮询频率，
	// 太大则单次失败影响面广、且占用数据库锁时间过长。
	batchSize = 100
	// idleInterval 无待投递事件时的轮询间隔
	idleInterval = 300 * time.Millisecond
	// busyInterval 仍有待投递事件时的轮询间隔，缩短以尽快追平积压
	busyInterval = 20 * time.Millisecond
	// publishTimeout 单条事件的投递超时
	publishTimeout = 5 * time.Second

	// maxRetry 最大重试次数，超过后标记为失败（死信），等待人工介入
	maxRetry = 10
	// baseBackoff 是重试退避的基准间隔。
	// 退避是必须的：下游持续不可用时若持续快速重试，
	// 会把下游打得更难恢复，也会白耗数据库与网络资源。
	baseBackoff = 2 * time.Second
	// maxBackoff 退避上限
	maxBackoff = 5 * time.Minute
)

// Relay 是 Outbox 投递协程。
type Relay struct {
	outbox    biz.OutboxRepo
	publisher mq.Publisher
	log       *klog.Helper
}

// New 构造投递协程。
func New(outbox biz.OutboxRepo, publisher mq.Publisher, logger klog.Logger) *Relay {
	return &Relay{
		outbox:    outbox,
		publisher: publisher,
		log:       klog.NewHelper(klog.With(logger, "module", "relay")),
	}
}

// Run 阻塞运行投递循环，直到 ctx 被取消。
//
// 由 Kratos 应用生命周期驱动：随服务启动而启动，随优雅退出而停止。
func (r *Relay) Run(ctx context.Context) {
	r.log.Infow("msg", "Outbox 投递协程已启动", "batchSize", batchSize)

	timer := time.NewTimer(idleInterval)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			r.log.Infow("msg", "Outbox 投递协程已停止")
			return
		case <-timer.C:
			n := r.poll(ctx)

			// 有积压时缩短间隔以尽快追平，空闲时放长间隔降低数据库压力
			interval := idleInterval
			if n >= batchSize {
				interval = busyInterval
			}
			timer.Reset(interval)
		}
	}
}

// poll 处理一批事件，返回实际处理的条数。
func (r *Relay) poll(ctx context.Context) int {
	events, err := r.outbox.FetchPending(ctx, batchSize)
	if err != nil {
		// 取事件失败通常是数据库抖动，记录后等下一轮即可，不应中断循环
		r.log.Errorw("msg", "捞取待投递事件失败", "err", err)
		return 0
	}

	for _, ev := range events {
		r.deliver(ctx, ev)
	}
	return len(events)
}

// deliver 投递单条事件并更新其状态。
func (r *Relay) deliver(ctx context.Context, ev *biz.OutboxEvent) {
	// 投递超时独立于外层 ctx：外层取消（服务退出）时也应让当前这条
	// 有机会完成或明确失败，而不是留下状态不明的记录。
	pubCtx, cancel := context.WithTimeout(ctx, publishTimeout)
	defer cancel()

	if err := r.publisher.Publish(pubCtx, ev.Topic, ev.PartitionKey, ev.Payload); err != nil {
		r.handleFailure(ctx, ev, err)
		return
	}

	if err := r.outbox.MarkDelivered(ctx, ev.EventID); err != nil {
		// 消息已投递但状态未更新：会造成下一轮重复投递。
		// 这是刻意的取舍——at-least-once 语义下重复投递由消费端幂等消化，
		// 而若为了"精确一次"改成先标状态再投递，则会变成丢消息。
		r.log.Errorw("msg", "事件已投递但状态更新失败，将导致重复投递", "eventId", ev.EventID, "err", err)
	}
}

// handleFailure 处理投递失败：安排退避重试，超过上限则落地为失败。
func (r *Relay) handleFailure(ctx context.Context, ev *biz.OutboxEvent, cause error) {
	// 退避时长随重试次数指数增长，并设上限
	backoff := baseBackoff
	if ev.RetryCount > 0 {
		if d := baseBackoff << uint(min(ev.RetryCount, 8)); d < maxBackoff {
			backoff = d
		} else {
			backoff = maxBackoff
		}
	}

	if err := r.outbox.MarkFailed(ctx, ev.EventID, cause.Error(), time.Now().Add(backoff)); err != nil {
		r.log.Errorw("msg", "标记事件投递失败时出错", "eventId", ev.EventID, "err", err)
		return
	}

	if ev.RetryCount+1 >= maxRetry {
		// 达到上限：标记为失败（死信），不再自动重试。
		// 此时必须告警——这类事件意味着对应消息在服务端存在但永远不会被推送，
		// 只能由客户端主动拉取补齐。
		r.log.Errorw("msg", "事件投递已达最大重试次数，转为死信",
			"eventId", ev.EventID, "topic", ev.Topic, "retryCount", ev.RetryCount+1, "err", cause)
		return
	}

	r.log.Warnw("msg", "事件投递失败，已安排重试",
		"eventId", ev.EventID, "retryCount", ev.RetryCount+1, "backoff", backoff.String(), "err", cause)
}
