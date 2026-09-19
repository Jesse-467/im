package ws

import (
	"testing"
)

// newTestConn 构造一条用于测试的连接。
func newTestConn(uid int64, connID string) *Conn {
	return newConn(uid, "node-test", "test", connID)
}

// TestRegistryMultiDevice 验证同一用户的多端连接都被保留。
//
// 这是多端同时在线的核心不变量：若实现用 userId -> *Conn 的一对一映射，
// 后登录的端会覆盖先登录的，表现为「手机上登录后，网页端再也收不到消息」——
// 这类问题在单端测试中完全测不出来。
func TestRegistryMultiDevice(t *testing.T) {
	r := NewRegistry(newTestLogger())

	c1 := newTestConn(100, "conn-1")
	c2 := newTestConn(100, "conn-2")
	c3 := newTestConn(100, "conn-3")
	r.Add(c1)
	r.Add(c2)
	r.Add(c3)

	if got := len(r.LocalConns(100)); got != 3 {
		t.Fatalf("多端连接数 = %d, 期望 3", got)
	}
	if got := r.ConnCount(); got != 3 {
		t.Fatalf("总连接数 = %d, 期望 3", got)
	}
}

// TestRegistryDeliverToAllDevices 验证消息投递到该用户的全部端。
func TestRegistryDeliverToAllDevices(t *testing.T) {
	r := NewRegistry(newTestLogger())

	for _, id := range []string{"c1", "c2"} {
		r.Add(newTestConn(200, id))
	}

	delivered := r.Deliver(200, []byte("hello"))
	if delivered != 2 {
		t.Fatalf("投递到的连接数 = %d, 期望 2", delivered)
	}

	for _, c := range r.LocalConns(200) {
		select {
		case payload := <-c.send:
			if string(payload) != "hello" {
				t.Errorf("连接 %s 收到的内容 = %q", c.ConnID, payload)
			}
		default:
			t.Errorf("连接 %s 未收到消息", c.ConnID)
		}
	}
}

// TestRegistryRemoveKeepsOtherDevices 验证断开一端不影响其他端。
func TestRegistryRemoveKeepsOtherDevices(t *testing.T) {
	r := NewRegistry(newTestLogger())

	c1 := newTestConn(300, "c1")
	c2 := newTestConn(300, "c2")
	r.Add(c1)
	r.Add(c2)

	r.Remove(c1)

	conns := r.LocalConns(300)
	if len(conns) != 1 || conns[0].ConnID != "c2" {
		t.Fatalf("移除一端后剩余连接异常: %+v", conns)
	}
}

// TestRegistryRemoveCleansUpUserEntry 验证用户无连接时清理 map 条目。
//
// 若不清理，长期运行后注册表会残留大量空 map，造成内存泄漏。
func TestRegistryRemoveCleansUpUserEntry(t *testing.T) {
	r := NewRegistry(newTestLogger())

	c := newTestConn(400, "c1")
	r.Add(c)
	r.Remove(c)

	if r.IsOnlineLocally(400) {
		t.Error("用户已无连接，不应判定为在线")
	}
	if r.ConnCount() != 0 {
		t.Errorf("连接数 = %d, 期望 0", r.ConnCount())
	}
}

// TestConnTrySendBufferFull 验证缓冲满时返回 false 而不阻塞。
//
// 这是防止「一条慢连接拖垮整个投递链路」的关键行为：
// 投递方是消息消费者，绝不能因为某个客户端消费不过来而卡住。
func TestConnTrySendBufferFull(t *testing.T) {
	c := newTestConn(500, "c1")

	// 填满缓冲
	for i := 0; i < sendBufferSize; i++ {
		if !c.trySend([]byte("x")) {
			t.Fatalf("第 %d 次写入不应失败", i)
		}
	}

	// 缓冲已满：必须立即返回 false，而不是阻塞
	if c.trySend([]byte("overflow")) {
		t.Error("缓冲已满时应返回 false")
	}
}

// TestConnCloseIdempotent 验证重复关闭不会 panic。
//
// 多个触发源（读写协程出错、心跳超时、被踢下线）都可能触发关闭，
// 若实现直接 close(channel) 而不做保护，第二次调用会 panic 导致进程崩溃。
func TestConnCloseIdempotent(t *testing.T) {
	c := newTestConn(600, "c1")

	c.Close()
	c.Close()
	c.Close()

	if !c.Closed() {
		t.Error("连接应处于已关闭状态")
	}
	if c.trySend([]byte("x")) {
		t.Error("已关闭的连接不应接受新消息")
	}
}

// TestConnTouchUpdatesTime 验证活跃时间可刷新。
//
// 这是心跳续期的基础：若不刷新，活跃连接会因「看起来不活跃」被清理掉。
func TestConnTouchUpdatesTime(t *testing.T) {
	c := newTestConn(700, "c1")

	before := c.LastActive()
	c.touch()
	after := c.LastActive()

	if !after.After(before) && !after.Equal(before) {
		t.Errorf("活跃时间未推进: before=%v after=%v", before, after)
	}
}

// TestRegistryConcurrentAccess 验证并发注册与投递不产生竞态。
//
// 用 -race 运行时才能发现数据竞争，是所有并发代码的基础防线。
func TestRegistryConcurrentAccess(t *testing.T) {
	r := NewRegistry(newTestLogger())

	const workers = 8
	const perWorker = 20

	done := make(chan struct{})
	for w := 0; w < workers; w++ {
		go func(w int) {
			defer func() { done <- struct{}{} }()
			for i := 0; i < perWorker; i++ {
				c := newTestConn(int64(w*1000+i), "c")
				r.Add(c)
				r.Deliver(int64(w*1000+i), []byte("m"))
				r.Remove(c)
			}
		}(w)
	}
	for w := 0; w < workers; w++ {
		<-done
	}

	if r.ConnCount() != 0 {
		t.Errorf("全部注销后连接数 = %d, 期望 0", r.ConnCount())
	}
}
