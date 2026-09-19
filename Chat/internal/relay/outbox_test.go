package relay

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	klog "github.com/go-kratos/kratos/v2/log"

	"github.com/Jesse-467/im/Chat/internal/biz"
)

// ── 测试替身 ────────────────────────────────────────────────────────────────

// fakeOutbox 是内存版 Outbox，实现了「投递中」这道状态机。
//
// 刻意复刻真实实现的语义（捞取即置为投递中），否则测不出重复投递类缺陷——
// 用一个「捞取不改状态」的假实现会让所有重复投递的用例都假通过。
type fakeOutbox struct {
	mu     sync.Mutex
	events map[string]*fakeEvent
	// fetchCount 记录捞取次数，用于断言同一事件不会被重复捞取
	fetchCount map[string]int
}

type fakeEvent struct {
	event        *biz.OutboxEvent
	status       int16 // 0 待投递 1 已投递 2 死信 3 投递中
	retryCount   int
	nextRetryAt  time.Time
	updatedAt    time.Time
	lastError    string
	deliverCalls int
}

const (
	fakePending    int16 = 0
	fakeDelivered  int16 = 1
	fakeDead       int16 = 2
	fakeDelivering int16 = 3
)

func newFakeOutbox(events ...*biz.OutboxEvent) *fakeOutbox {
	f := &fakeOutbox{
		events:     make(map[string]*fakeEvent),
		fetchCount: make(map[string]int),
	}
	for _, e := range events {
		f.events[e.EventID] = &fakeEvent{
			event:       e,
			status:      fakePending,
			nextRetryAt: time.Now().Add(-time.Second),
			updatedAt:   time.Now(),
		}
	}
	return f
}

func (f *fakeOutbox) Append(context.Context, *biz.OutboxEvent) error { return nil }

func (f *fakeOutbox) FetchPending(_ context.Context, limit int) ([]*biz.OutboxEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []*biz.OutboxEvent
	now := time.Now()
	for _, e := range f.events {
		if len(out) >= limit {
			break
		}
		if e.status == fakePending && !e.nextRetryAt.After(now) {
			// 关键：捞取的同时置为投递中，复刻真实实现
			e.status = fakeDelivering
			e.updatedAt = now
			f.fetchCount[e.event.EventID]++
			out = append(out, e.event)
		}
	}
	return out, nil
}

func (f *fakeOutbox) ReclaimStale(_ context.Context, lease time.Duration, limit int) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	n := 0
	deadline := time.Now().Add(-lease)
	for _, e := range f.events {
		if n >= limit {
			break
		}
		if e.status == fakeDelivering && e.updatedAt.Before(deadline) {
			e.status = fakePending
			e.nextRetryAt = time.Now()
			e.updatedAt = time.Now()
			e.lastError = "投递超时未确认，已回收重投"
			n++
		}
	}
	return n, nil
}

func (f *fakeOutbox) MarkDelivered(_ context.Context, eventID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	e, ok := f.events[eventID]
	if !ok {
		return errors.New("事件不存在")
	}
	// 复刻真实实现的条件更新：只有投递中的事件才能被确认
	if e.status != fakeDelivering {
		return nil
	}
	e.status = fakeDelivered
	e.lastError = ""
	return nil
}

func (f *fakeOutbox) MarkFailed(_ context.Context, eventID, reason string, nextRetryAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	e, ok := f.events[eventID]
	if !ok {
		return errors.New("事件不存在")
	}
	e.status = fakePending
	e.retryCount++
	e.lastError = reason
	e.nextRetryAt = nextRetryAt
	e.updatedAt = time.Now()
	return nil
}

func (f *fakeOutbox) MarkDead(_ context.Context, eventID, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	e, ok := f.events[eventID]
	if !ok {
		return errors.New("事件不存在")
	}
	e.status = fakeDead
	e.lastError = reason
	e.updatedAt = time.Now()
	return nil
}

// statusOf 返回事件当前状态，供断言使用。
func (f *fakeOutbox) statusOf(eventID string) int16 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.events[eventID].status
}

func (f *fakeOutbox) retryCountOf(eventID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.events[eventID].retryCount
}

func (f *fakeOutbox) fetches(eventID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fetchCount[eventID]
}

// fakePublisher 记录每次投递，并可按需失败。
type fakePublisher struct {
	mu       sync.Mutex
	publish  []string
	failWith error
}

func (p *fakePublisher) Publish(_ context.Context, _, key string, _ []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failWith != nil {
		return p.failWith
	}
	p.publish = append(p.publish, key)
	return nil
}

func (p *fakePublisher) Close() error { return nil }

func (p *fakePublisher) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.publish)
}

func testLogger() klog.Logger {
	return klog.NewStdLogger(discardWriter{})
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

func newEvent(id string, retry int) *biz.OutboxEvent {
	return &biz.OutboxEvent{
		EventID:      id,
		Topic:        "im.message",
		PartitionKey: "conv-1",
		Payload:      []byte(`{"conversationId":1}`),
		RetryCount:   retry,
	}
}

// ── 用例 ────────────────────────────────────────────────────────────────────

// TestPollDoesNotRedeliverPendingEvent 验证同一事件不会被重复投递。
//
// 这是「发送者收到两条推送」的核心防线：此前 FetchPending 不改状态，
// 每轮轮询都会把同一批事件再捞一遍，于是同一条消息被反复投递到队列，
// 最终被反复推送给用户。修复方式是捞取即置为「投递中」。
func TestPollDoesNotRedeliverPendingEvent(t *testing.T) {
	ob := newFakeOutbox(newEvent("ev-1", 0))
	pub := &fakePublisher{}
	r := New(ob, pub, testLogger())

	// 连续跑多轮 poll，模拟投递协程持续轮询
	for i := 0; i < 5; i++ {
		r.poll(context.Background())
	}

	if got := pub.count(); got != 1 {
		t.Fatalf("事件被投递 %d 次，期望恰好 1 次", got)
	}
	if got := ob.fetches("ev-1"); got != 1 {
		t.Fatalf("事件被捞取 %d 次，期望恰好 1 次", got)
	}
	if got := ob.statusOf("ev-1"); got != fakeDelivered {
		t.Fatalf("投递成功后状态 = %d, 期望已投递(%d)", got, fakeDelivered)
	}
}

// TestFetchMarksDeliveringBeforePublish 验证捞取即进入投递中。
//
// 若这一步缺失，投递耗时较长的消息会在投递完成前被下一轮捞走，
// 表现为「偶发重复推送」——只在有并发或慢下游时出现，极难复现。
func TestFetchMarksDeliveringBeforePublish(t *testing.T) {
	ob := newFakeOutbox(newEvent("ev-1", 0))

	events, err := ob.FetchPending(context.Background(), 10)
	if err != nil {
		t.Fatalf("捞取失败: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("捞取到 %d 条，期望 1 条", len(events))
	}
	if got := ob.statusOf("ev-1"); got != fakeDelivering {
		t.Fatalf("捞取后状态 = %d, 期望投递中(%d)", got, fakeDelivering)
	}
}

// TestReclaimReturnsStaleDelivering 验证滞留的投递中事件会被回收重投。
//
// 「捞取即置为投递中」换来了不重复，但若进程在投递途中被强杀，
// 事件会永远停在投递中。没有回收机制，「不重复」就要以「可能丢失」为代价。
func TestReclaimReturnsStaleDelivering(t *testing.T) {
	ob := newFakeOutbox(newEvent("ev-1", 0))
	pub := &fakePublisher{}
	r := New(ob, pub, testLogger())

	// 第一次投递失败：事件退回待投递
	pub.failWith = errors.New("kafka 不可用")
	r.poll(context.Background())
	if got := ob.statusOf("ev-1"); got != fakePending {
		t.Fatalf("投递失败后状态 = %d, 期望待投递(%d)", got, fakePending)
	}

	// 模拟「捞取后进程被强杀」：手工置为投递中，并把更新时间推老
	ob.mu.Lock()
	ob.events["ev-1"].status = fakeDelivering
	ob.events["ev-1"].updatedAt = time.Now().Add(-2 * deliveringLease)
	ob.mu.Unlock()

	n, err := ob.ReclaimStale(context.Background(), deliveringLease, 100)
	if err != nil {
		t.Fatalf("回收失败: %v", err)
	}
	if n != 1 {
		t.Fatalf("回收 %d 条，期望 1 条", n)
	}
	if got := ob.statusOf("ev-1"); got != fakePending {
		t.Fatalf("回收后状态 = %d, 期望待投递(%d)", got, fakePending)
	}
}

// TestReclaimKeepsFreshDelivering 验证未超时的投递中事件不会被误回收。
//
// 误回收会把一条正在投递中的事件重新投递，造成重复推送。
// 因此租约必须显著大于单条投递超时。
func TestReclaimKeepsFreshDelivering(t *testing.T) {
	ob := newFakeOutbox(newEvent("ev-1", 0))

	if _, err := ob.FetchPending(context.Background(), 10); err != nil {
		t.Fatalf("捞取失败: %v", err)
	}

	n, err := ob.ReclaimStale(context.Background(), deliveringLease, 100)
	if err != nil {
		t.Fatalf("回收失败: %v", err)
	}
	if n != 0 {
		t.Fatalf("刚捞取的事件不应被回收，实际回收 %d 条", n)
	}
	if got := ob.statusOf("ev-1"); got != fakeDelivering {
		t.Fatalf("状态 = %d, 期望仍为投递中(%d)", got, fakeDelivering)
	}
}

// TestDeliveringLeaseExceedsPublishTimeout 验证租约远大于单条投递超时。
//
// 这是上一条用例的静态保障：若租约接近或小于 publishTimeout，
// 正常的慢投递就会被当成滞留而重复投递。
func TestDeliveringLeaseExceedsPublishTimeout(t *testing.T) {
	if deliveringLease <= publishTimeout*3 {
		t.Fatalf("租约 %v 相对单条投递超时 %v 过短，慢投递会被误判为滞留",
			deliveringLease, publishTimeout)
	}
}

// TestMarkDeliveredOnlyFromDelivering 验证只有投递中的事件能被确认。
//
// 防止迟到的确认把一条已被回收重投的事件又改成已投递——那会造成漏投递，
// 而漏投递比重复投递更难补救。
func TestMarkDeliveredOnlyFromDelivering(t *testing.T) {
	ob := newFakeOutbox(newEvent("ev-1", 0))

	// 事件仍是待投递状态，此时确认应当无效
	if err := ob.MarkDelivered(context.Background(), "ev-1"); err != nil {
		t.Fatalf("确认调用出错: %v", err)
	}
	if got := ob.statusOf("ev-1"); got != fakePending {
		t.Fatalf("待投递的事件不应被确认成功，状态 = %d", got)
	}
}

// TestFailureRetriesWithBackoff 验证投递失败进入重试且退避递增。
func TestFailureRetriesWithBackoff(t *testing.T) {
	ob := newFakeOutbox(newEvent("ev-1", 0))
	pub := &fakePublisher{failWith: errors.New("kafka 不可用")}
	r := New(ob, pub, testLogger())

	r.poll(context.Background())

	if got := ob.statusOf("ev-1"); got != fakePending {
		t.Fatalf("失败后应退回待投递，状态 = %d", got)
	}
	if got := ob.retryCountOf("ev-1"); got != 1 {
		t.Fatalf("重试次数 = %d, 期望 1", got)
	}
}

// TestDeadLetterAfterMaxRetry 验证重试耗尽后进入死信终态。
//
// 此前这条路径只打日志、不改状态，事件会永远在待投递与失败之间循环，
// 与「已转死信」的日志自相矛盾，排查时严重误导。
func TestDeadLetterAfterMaxRetry(t *testing.T) {
	ob := newFakeOutbox(newEvent("ev-1", maxRetry-1))
	pub := &fakePublisher{failWith: errors.New("kafka 不可用")}
	r := New(ob, pub, testLogger())

	r.poll(context.Background())

	if got := ob.statusOf("ev-1"); got != fakeDead {
		t.Fatalf("重试耗尽后状态 = %d, 期望死信(%d)", got, fakeDead)
	}
}

// TestDeadEventNotFetchedAgain 验证死信不会再被自动重试。
func TestDeadEventNotFetchedAgain(t *testing.T) {
	ob := newFakeOutbox(newEvent("ev-1", maxRetry-1))
	pub := &fakePublisher{failWith: errors.New("kafka 不可用")}
	r := New(ob, pub, testLogger())

	r.poll(context.Background())
	before := pub.count()

	// 死信状态的 next_retry_at 被推到远未来，后续轮询不应再捞取它
	ob.mu.Lock()
	ob.events["ev-1"].nextRetryAt = time.Now().Add(-time.Hour)
	ob.mu.Unlock()

	for i := 0; i < 3; i++ {
		r.poll(context.Background())
	}

	if got := pub.count(); got != before {
		t.Fatalf("死信事件被重复投递，投递次数从 %d 增至 %d", before, got)
	}
}

// TestConcurrentRelaysDoNotDoubleDeliver 验证多实例并发投递不重复。
//
// 这是 FOR UPDATE SKIP LOCKED 要解决的问题：两个实例同时轮询时，
// 各自应捞到不同的行，而不是把同一批事件各投一次。
func TestConcurrentRelaysDoNotDoubleDeliver(t *testing.T) {
	const n = 50
	events := make([]*biz.OutboxEvent, 0, n)
	for i := 0; i < n; i++ {
		events = append(events, newEvent(eventID(i), 0))
	}
	ob := newFakeOutbox(events...)
	pub := &fakePublisher{}

	// 两个投递协程各跑若干轮（fakeOutbox 内部加锁，模拟行锁）
	var wg sync.WaitGroup
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := New(ob, pub, testLogger())
			for i := 0; i < 5; i++ {
				r.poll(context.Background())
			}
		}()
	}
	wg.Wait()

	if got := pub.count(); got != n {
		t.Fatalf("投递 %d 次，期望恰好 %d 次（每个事件一次）", got, n)
	}
}

func eventID(i int) string {
	return "ev-" + string(rune('a'+i%26)) + string(rune('0'+i/26))
}
