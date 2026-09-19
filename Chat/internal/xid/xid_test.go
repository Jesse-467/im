package xid

import (
	"testing"
	"time"
)

// TestNextUniqueAndIncreasing 验证连续发号唯一且趋势递增。
func TestNextUniqueAndIncreasing(t *testing.T) {
	g, err := New(1)
	if err != nil {
		t.Fatalf("构造生成器失败: %v", err)
	}

	const n = 5000
	seen := make(map[int64]struct{}, n)
	var prev int64

	for i := 0; i < n; i++ {
		id, err := g.Next()
		if err != nil {
			t.Fatalf("第 %d 次发号失败: %v", i, err)
		}
		if id <= 0 {
			t.Fatalf("第 %d 次生成非正数 ID: %d", i, id)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("第 %d 次生成重复 ID: %d", i, id)
		}
		seen[id] = struct{}{}

		// 时间戳单调不减，因此 ID 必须严格递增（同毫秒内靠序列位递增）
		if id <= prev {
			t.Fatalf("ID 未递增: 第 %d 次 %d <= 上一次 %d", i, id, prev)
		}
		prev = id
	}
}

// TestNextConcurrent 验证并发发号不重复。
//
// 这是生成器最关键的属性：ID 一旦重复，会话或消息就会互相覆盖，
// 且这类问题只在并发下出现，单线程测试测不出来。必须配合 -race 运行。
func TestNextConcurrent(t *testing.T) {
	g, err := New(2)
	if err != nil {
		t.Fatalf("构造生成器失败: %v", err)
	}

	const workers = 16
	const perWorker = 500

	results := make(chan int64, workers*perWorker)
	done := make(chan struct{})

	for w := 0; w < workers; w++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for i := 0; i < perWorker; i++ {
				id, err := g.Next()
				if err != nil {
					t.Errorf("并发发号失败: %v", err)
					return
				}
				results <- id
			}
		}()
	}

	for w := 0; w < workers; w++ {
		<-done
	}
	close(results)

	seen := make(map[int64]struct{}, workers*perWorker)
	for id := range results {
		if _, dup := seen[id]; dup {
			t.Fatalf("并发下产生重复 ID: %d", id)
		}
		seen[id] = struct{}{}
	}
	if len(seen) != workers*perWorker {
		t.Fatalf("唯一 ID 数 = %d, 期望 %d", len(seen), workers*perWorker)
	}
}

// TestNewRejectsInvalidNodeID 验证节点号越界被拒绝。
//
// 越界的节点号会与时间位或序列位重叠，导致不同节点生成相同 ID，
// 因此必须在构造时就拦住，而不是等到产生重复数据。
func TestNewRejectsInvalidNodeID(t *testing.T) {
	if _, err := New(maxNodeID + 1); err == nil {
		t.Error("超出上限的 nodeID 应被拒绝")
	}
	if _, err := New(-1); err == nil {
		t.Error("负数 nodeID 应被拒绝")
	}
	if _, err := New(maxNodeID); err != nil {
		t.Errorf("边界值 nodeID=%d 应被接受，得到 %v", maxNodeID, err)
	}
}

// TestNewDerivesNodeIDWhenZero 验证 nodeID 为 0 时自动推导。
//
// 推导出的节点号必须在合法区间内，否则发号会产出越界 ID。
func TestNewDerivesNodeIDWhenZero(t *testing.T) {
	g, err := New(0)
	if err != nil {
		t.Fatalf("构造生成器失败: %v", err)
	}
	if g.nodeID < 0 || g.nodeID > maxNodeID {
		t.Fatalf("推导出的 nodeID 越界: %d", g.nodeID)
	}
	if _, err := g.Next(); err != nil {
		t.Fatalf("推导模式下发号失败: %v", err)
	}
}

// TestNextDetectsClockBackwards 验证时钟回拨被显式检出。
//
// 静默产生重复 ID 比报错危险得多：重复的会话 ID 会让两个会话互相覆盖，
// 而且事后无法从数据上区分。因此宁可让调用方拿到错误。
func TestNextDetectsClockBackwards(t *testing.T) {
	g, err := New(3)
	if err != nil {
		t.Fatalf("构造生成器失败: %v", err)
	}

	if _, err := g.Next(); err != nil {
		t.Fatalf("首次发号失败: %v", err)
	}

	// 人为把 lastTime 推到未来，模拟时钟回拨
	g.mu.Lock()
	g.lastTime = time.Now().UnixMilli() - epoch + 60_000
	g.mu.Unlock()

	if _, err := g.Next(); err == nil {
		t.Fatal("时钟回拨时应当返回错误")
	}
}

// TestNextExhaustsSequenceWithinSameMillisecond 验证同一毫秒内序列用尽后自旋。
//
// 同一毫秒最多发 4096 个号，超出后必须等到下一毫秒，
// 而不是把序列位回绕导致重复。
func TestNextExhaustsSequenceWithinSameMillisecond(t *testing.T) {
	g, err := New(4)
	if err != nil {
		t.Fatalf("构造生成器失败: %v", err)
	}

	// 固定时间基准，让 next 在同一毫秒内把序列耗尽
	g.mu.Lock()
	base := time.Now().UnixMilli() - epoch
	g.lastTime = base - 1
	g.sequence = maxSequence
	g.mu.Unlock()

	// 第一次：now > lastTime，序列归零
	first, err := g.Next()
	if err != nil {
		t.Fatalf("发号失败: %v", err)
	}
	// 第二次：now == lastTime 且序列已达上限，应自旋到下一毫秒
	second, err := g.Next()
	if err != nil {
		t.Fatalf("发号失败: %v", err)
	}
	if second <= first {
		t.Fatalf("序列用尽后应等待下一毫秒，ID 未递增: %d -> %d", first, second)
	}
}

// TestMustNextPanicsOnFailure 验证 MustNext 在失败时 panic 而非返回 0。
//
// 返回 0 会被静默当成合法 ID 写进数据库，属于最危险的一类失败。
func TestMustNextPanicsOnFailure(t *testing.T) {
	g, err := New(5)
	if err != nil {
		t.Fatalf("构造生成器失败: %v", err)
	}
	g.mu.Lock()
	g.lastTime = time.Now().UnixMilli() - epoch + 60_000
	g.mu.Unlock()

	defer func() {
		if r := recover(); r == nil {
			t.Error("MustNext 在发号失败时应当 panic")
		}
	}()
	_ = g.MustNext()
}
