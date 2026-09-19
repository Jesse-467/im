package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	klog "github.com/go-kratos/kratos/v2/log"

	"github.com/Jesse-467/im/Chat/internal/biz"
	"github.com/Jesse-467/im/Chat/internal/ws"
)

// ── 测试替身 ────────────────────────────────────────────────────────────────

type fakeConvRepo struct {
	members []*biz.ConversationMember
	err     error
}

func (f *fakeConvRepo) Create(context.Context, *biz.Conversation, []*biz.ConversationMember) error {
	return nil
}
func (f *fakeConvRepo) FindByID(context.Context, int64) (*biz.Conversation, error) { return nil, nil }
func (f *fakeConvRepo) FindByBizKey(context.Context, int32, string) (*biz.Conversation, error) {
	return nil, nil
}
func (f *fakeConvRepo) ListByUser(context.Context, int64) ([]*biz.UserConversation, error) {
	return nil, nil
}
func (f *fakeConvRepo) FindPeerIDs(context.Context, []int64, int64) (map[int64]int64, error) {
	return nil, nil
}
func (f *fakeConvRepo) AddMembers(context.Context, int64, []*biz.ConversationMember) (int, error) {
	return 0, nil
}
func (f *fakeConvRepo) RemoveMember(context.Context, int64, int64) error { return nil }
func (f *fakeConvRepo) FindMember(context.Context, int64, int64) (*biz.ConversationMember, error) {
	return nil, nil
}
func (f *fakeConvRepo) ListMembers(context.Context, int64) ([]*biz.ConversationMember, error) {
	return f.members, f.err
}
func (f *fakeConvRepo) UpdateMemberReadSeq(context.Context, int64, int64, int64) error { return nil }
func (f *fakeConvRepo) UpdateMemberAlias(context.Context, int64, int64, string) error  { return nil }
func (f *fakeConvRepo) UpdateMaxSeq(context.Context, int64, int64) error               { return nil }
func (f *fakeConvRepo) Update(context.Context, *biz.Conversation) error                { return nil }

// fakeLoader 按 (会话, seq) 返回预置消息。
type fakeLoader struct {
	mu       sync.Mutex
	messages map[string]*biz.Message
	calls    int
	err      error
}

func (l *fakeLoader) LoadMessage(_ context.Context, conversationID, seq int64) (*biz.Message, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++
	if l.err != nil {
		return nil, l.err
	}
	return l.messages[msgKey(conversationID, seq)], nil
}

func (l *fakeLoader) callCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.calls
}

// fakePusher 记录推送次数。
type fakePusher struct {
	mu      sync.Mutex
	online  map[int64]bool
	pushes  []int64
	payload []ws.Message
}

func (p *fakePusher) PushToUser(userID int64, msg ws.Message) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pushes = append(p.pushes, userID)
	p.payload = append(p.payload, msg)
}

func (p *fakePusher) IsOnlineLocally(userID int64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.online[userID]
}

func (p *fakePusher) pushCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.pushes)
}

func testLogger() klog.Logger { return klog.NewStdLogger(discardWriter{}) }

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// newTestConsumer 构造一个位于单聊会话的消费者。
func newTestConsumer(loader *fakeLoader, pusher *fakePusher) *Consumer {
	repo := &fakeConvRepo{members: []*biz.ConversationMember{
		{ConversationID: 1, UserID: 100},
		{ConversationID: 1, UserID: 200},
	}}
	return New(repo, loader, pusher, testLogger())
}

// eventFor 生成一条投递事件载荷。
func eventFor(conversationID, seq int64) []byte {
	return []byte(`{"conversationId":` + itoa(conversationID) +
		`,"messageId":1,"seq":` + itoa(seq) + `,"senderId":100,"type":1}`)
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// ── 用例 ────────────────────────────────────────────────────────────────────

// TestHandlePushesToOnlineMembers 验证消息推送给会话内的在线成员。
func TestHandlePushesToOnlineMembers(t *testing.T) {
	loader := &fakeLoader{messages: map[string]*biz.Message{
		msgKey(1, 5): {ID: 1, ConversationID: 1, Seq: 5, SenderID: 100, Content: "hi"},
	}}
	pusher := &fakePusher{online: map[int64]bool{100: true, 200: true}}
	c := newTestConsumer(loader, pusher)

	if err := c.Handle(context.Background(), "im.message", "1", eventFor(1, 5)); err != nil {
		t.Fatalf("处理失败: %v", err)
	}
	if got := pusher.pushCount(); got != 2 {
		t.Fatalf("推送 %d 次，期望 2 次（两个成员都在线）", got)
	}
}

// TestHandleSkipsOfflineMembers 验证不在线的成员不会被推送。
//
// 推送只针对本节点有连接的成员，其他节点上的成员由那些节点自己推送。
func TestHandleSkipsOfflineMembers(t *testing.T) {
	loader := &fakeLoader{messages: map[string]*biz.Message{
		msgKey(1, 5): {ID: 1, ConversationID: 1, Seq: 5, SenderID: 100},
	}}
	pusher := &fakePusher{online: map[int64]bool{200: true}}
	c := newTestConsumer(loader, pusher)

	if err := c.Handle(context.Background(), "im.message", "1", eventFor(1, 5)); err != nil {
		t.Fatalf("处理失败: %v", err)
	}
	if got := pusher.pushCount(); got != 1 {
		t.Fatalf("推送 %d 次，期望 1 次（只有 200 在线）", got)
	}
}

// TestHandleIsIdempotent 验证同一事件重复投递只推送一次。
//
// 这是「发送者收到两条推送」在消费侧的防线。投递语义是 at-least-once，
// 重复可能来自 Outbox 回收重投，也可能来自 Kafka 自身。
// 客户端虽然也会去重，但服务端不重复推送能省带宽、也避免界面闪烁。
func TestHandleIsIdempotent(t *testing.T) {
	loader := &fakeLoader{messages: map[string]*biz.Message{
		msgKey(1, 5): {ID: 1, ConversationID: 1, Seq: 5, SenderID: 100},
	}}
	pusher := &fakePusher{online: map[int64]bool{100: true}}
	c := newTestConsumer(loader, pusher)

	payload := eventFor(1, 5)
	for i := 0; i < 3; i++ {
		if err := c.Handle(context.Background(), "im.message", "1", payload); err != nil {
			t.Fatalf("第 %d 次处理失败: %v", i, err)
		}
	}

	if got := pusher.pushCount(); got != 1 {
		t.Fatalf("推送 %d 次，期望恰好 1 次", got)
	}
	// 重复投递应当被拦在加载之前，连查询都省掉
	if got := loader.callCount(); got != 1 {
		t.Fatalf("加载消息 %d 次，期望恰好 1 次", got)
	}
}

// TestDifferentSeqNotDeduped 验证不同序号的消息不会被误去重。
//
// 去重键必须包含 seq，否则同一会话的第二条消息会被当成重复而丢掉——
// 这会造成真实的消息丢失，比重复推送严重得多。
func TestDifferentSeqNotDeduped(t *testing.T) {
	loader := &fakeLoader{messages: map[string]*biz.Message{
		msgKey(1, 5): {ID: 1, ConversationID: 1, Seq: 5, SenderID: 100},
		msgKey(1, 6): {ID: 2, ConversationID: 1, Seq: 6, SenderID: 100},
		msgKey(1, 7): {ID: 3, ConversationID: 1, Seq: 7, SenderID: 100},
	}}
	pusher := &fakePusher{online: map[int64]bool{100: true}}
	c := newTestConsumer(loader, pusher)

	for _, seq := range []int64{5, 6, 7} {
		if err := c.Handle(context.Background(), "im.message", "1", eventFor(1, seq)); err != nil {
			t.Fatalf("seq=%d 处理失败: %v", seq, err)
		}
	}

	if got := pusher.pushCount(); got != 3 {
		t.Fatalf("推送 %d 次，期望 3 次", got)
	}
}

// TestDifferentConversationNotDeduped 验证不同会话的相同序号互不影响。
//
// 每个会话都从 seq=1 开始，若去重键漏了会话维度，
// 第二个会话的第一条消息就会被误判为重复而永远推不出去。
func TestDifferentConversationNotDeduped(t *testing.T) {
	loader := &fakeLoader{messages: map[string]*biz.Message{
		msgKey(1, 1): {ID: 1, ConversationID: 1, Seq: 1, SenderID: 100},
		msgKey(2, 1): {ID: 2, ConversationID: 2, Seq: 1, SenderID: 100},
	}}
	repo := &fakeConvRepo{members: []*biz.ConversationMember{{ConversationID: 1, UserID: 100}}}
	pusher := &fakePusher{online: map[int64]bool{100: true}}
	c := New(repo, loader, pusher, testLogger())

	for _, convID := range []int64{1, 2} {
		if err := c.Handle(context.Background(), "im.message", "k", eventFor(convID, 1)); err != nil {
			t.Fatalf("会话 %d 处理失败: %v", convID, err)
		}
	}

	if got := pusher.pushCount(); got != 2 {
		t.Fatalf("推送 %d 次，期望 2 次（两个会话各一次）", got)
	}
}

// TestLoadFailureAllowsRetry 验证加载失败后重试仍然有效。
//
// 去重键必须在处理失败时被放行。若先登记后处理、失败也不撤销，
// 一次瞬时故障就会让这条消息永远无法重推——把「去重」变成了「丢消息」。
func TestLoadFailureAllowsRetry(t *testing.T) {
	loader := &fakeLoader{
		messages: map[string]*biz.Message{
			msgKey(1, 5): {ID: 1, ConversationID: 1, Seq: 5, SenderID: 100},
		},
		err: errors.New("数据库暂时不可用"),
	}
	pusher := &fakePusher{online: map[int64]bool{100: true}}
	c := newTestConsumer(loader, pusher)

	payload := eventFor(1, 5)

	// 第一次：加载失败
	if err := c.Handle(context.Background(), "im.message", "1", payload); err == nil {
		t.Fatal("加载失败时应返回错误")
	}
	if got := pusher.pushCount(); got != 0 {
		t.Fatalf("加载失败不应推送，实际推送 %d 次", got)
	}

	// 故障恢复后重试应能成功
	loader.mu.Lock()
	loader.err = nil
	loader.mu.Unlock()

	if err := c.Handle(context.Background(), "im.message", "1", payload); err != nil {
		t.Fatalf("重试失败: %v", err)
	}
	if got := pusher.pushCount(); got != 1 {
		t.Fatalf("重试后应推送 1 次，实际 %d 次", got)
	}
}

// TestMalformedEventIgnored 验证格式错误的载荷被丢弃而非报错。
//
// 格式不兼容（如灰度期间的版本差异）重试也不会成功，
// 返回错误只会让这条坏消息反复占用消费循环。
func TestMalformedEventIgnored(t *testing.T) {
	loader := &fakeLoader{messages: map[string]*biz.Message{}}
	pusher := &fakePusher{online: map[int64]bool{}}
	c := newTestConsumer(loader, pusher)

	cases := [][]byte{
		[]byte(`not json`),
		[]byte(`{"conversationId":0,"seq":5}`),
		[]byte(`{"conversationId":1,"seq":0}`),
	}
	for i, payload := range cases {
		if err := c.Handle(context.Background(), "im.message", "k", payload); err != nil {
			t.Errorf("第 %d 条畸形载荷应被忽略，却返回错误: %v", i, err)
		}
	}
}

// TestPushedPayloadUsesStringIDs 验证下行推送的 ID 是字符串。
//
// 与 HTTP 接口保持一致的表示，客户端不需要为两条链路写两套解析逻辑，
// 也不会因雪花 ID 被 JS 舍入而拿到错误的会话 ID。
func TestPushedPayloadUsesStringIDs(t *testing.T) {
	const snowflake = int64(359572627845451776)
	loader := &fakeLoader{messages: map[string]*biz.Message{
		msgKey(snowflake, 5): {
			ID:             snowflake + 1,
			ConversationID: snowflake,
			Seq:            5,
			SenderID:       snowflake + 2,
			Content:        "hi",
		},
	}}
	repo := &fakeConvRepo{members: []*biz.ConversationMember{{ConversationID: snowflake, UserID: 100}}}
	pusher := &fakePusher{online: map[int64]bool{100: true}}
	c := New(repo, loader, pusher, testLogger())

	payload := []byte(`{"conversationId":359572627845451776,"messageId":1,"seq":5,"senderId":100,"type":1}`)
	if err := c.Handle(context.Background(), "im.message", "k", payload); err != nil {
		t.Fatalf("处理失败: %v", err)
	}
	if pusher.pushCount() != 1 {
		t.Fatalf("应推送 1 次，实际 %d 次", pusher.pushCount())
	}

	data, err := json.Marshal(pusher.payload[0])
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	for _, want := range []string{
		`"conversationId":"359572627845451776"`,
		`"messageId":"359572627845451777"`,
		`"senderId":"359572627845451778"`,
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("下行载荷缺少 %s，实际: %s", want, data)
		}
	}
}

// TestDedupCacheEvictsOldest 验证超出容量后按写入顺序淘汰。
//
// 淘汰策略必须保证内存有界：不设上限的去重表在长跑服务里
// 会随消息量无限增长，最终 OOM。
//
// 断言的是「先后被淘汰的是谁」而不是具体实现细节：
// 先写入的键先失去去重保护，最近写入的键仍受保护。
func TestDedupCacheEvictsOldest(t *testing.T) {
	d := newDedupCache(3)

	for _, k := range []string{"a", "b", "c"} {
		if !d.enter(k) {
			t.Fatalf("首次登记 %s 应返回 true", k)
		}
	}
	if got := d.size(); got != 3 {
		t.Fatalf("记录数 = %d, 期望 3", got)
	}

	// 写入第 4 个：最老的 "a" 被淘汰，容量保持不变
	d.enter("d")
	if got := d.size(); got != 3 {
		t.Fatalf("淘汰后记录数 = %d, 期望仍为 3", got)
	}
	if !d.enter("a") {
		t.Error("已被淘汰的键应可重新登记")
	}

	// 最近写入的 "d" 仍应受去重保护
	if d.enter("d") {
		t.Error("最近写入的键应仍被视为重复")
	}
}

// TestDedupCacheBoundedMemory 验证容量上限始终被遵守。
//
// 这是内存有界性的直接保证：无论写入多少不同的键，
// 内部记录数都不会超过容量。
func TestDedupCacheBoundedMemory(t *testing.T) {
	const capacity = 100
	d := newDedupCache(capacity)

	for i := 0; i < capacity*10; i++ {
		d.enter(msgKey(1, int64(i)))
	}

	if got := d.size(); got != capacity {
		t.Fatalf("记录数 = %d, 期望不超过容量 %d", got, capacity)
	}
}

// TestDedupCacheLeave 验证 leave 后可重新登记。
func TestDedupCacheLeave(t *testing.T) {
	d := newDedupCache(10)

	if !d.enter("k") {
		t.Fatal("首次登记应为 true")
	}
	if d.enter("k") {
		t.Fatal("重复登记应为 false")
	}

	d.leave("k")

	if !d.enter("k") {
		t.Fatal("leave 之后应可重新登记")
	}
}

// TestDedupCacheConcurrent 验证并发登记不产生竞态。
func TestDedupCacheConcurrent(t *testing.T) {
	d := newDedupCache(1000)

	const workers = 8
	const perWorker = 100

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				d.enter(msgKey(int64(w), int64(i)))
			}
		}(w)
	}
	wg.Wait()

	// 各 worker 的键互不相同，因此都应被保留（未超容量）
	if got := d.size(); got != workers*perWorker {
		t.Fatalf("记录数 = %d, 期望 %d", got, workers*perWorker)
	}
}

// TestDedupCacheSameKeyConcurrent 验证同一键并发登记只有一个成功。
//
// 这是幂等的核心：多协程同时处理同一事件时，只能有一个真正去推送。
func TestDedupCacheSameKeyConcurrent(t *testing.T) {
	d := newDedupCache(100)

	const workers = 16
	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if d.enter("same-key") {
				mu.Lock()
				success++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if success != 1 {
		t.Fatalf("同一键有 %d 次登记成功，期望恰好 1 次", success)
	}
}
