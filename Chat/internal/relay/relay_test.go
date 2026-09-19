package relay

import (
	"testing"
	"time"
)

// TestBackoffGrowsExponentially 验证退避时长随重试次数指数增长。
//
// 退避不增长会让下游在故障期间持续承压，难以自愈；
// 这里逐次比对，确保每一步都至少是前一步的两倍（未触及上限时）。
func TestBackoffGrowsExponentially(t *testing.T) {
	prev := Backoff(0)
	if prev != baseBackoff {
		t.Fatalf("首次退避 = %v, 期望 %v", prev, baseBackoff)
	}

	for i := 1; i < 8; i++ {
		cur := Backoff(i)
		if cur < prev*2 && cur != maxBackoff {
			t.Fatalf("第 %d 次退避 = %v, 未达到上一次 %v 的两倍", i, cur, prev)
		}
		if cur <= 0 {
			t.Fatalf("第 %d 次退避为非正数: %v", i, cur)
		}
		prev = cur
	}
}

// TestBackoffIncreasesMonotonically 验证退避时长单调不减。
func TestBackoffIncreasesMonotonically(t *testing.T) {
	prev := time.Duration(0)
	for i := 0; i <= 30; i++ {
		cur := Backoff(i)
		if cur < prev {
			t.Fatalf("第 %d 次退避回退: %v < %v", i, cur, prev)
		}
		prev = cur
	}
}

// TestBackoffCapped 验证退避存在上限。
//
// 无上限的指数退避最终会把间隔拉到数年，等价于消息被永久搁置，
// 因此必须有封顶。
func TestBackoffCapped(t *testing.T) {
	for _, i := range []int{10, 20, 50, 100, 1000} {
		d := Backoff(i)
		if d > maxBackoff {
			t.Fatalf("第 %d 次退避 %v 超过上限 %v", i, d, maxBackoff)
		}
	}
	if Backoff(1000) != maxBackoff {
		t.Errorf("极大重试次数应取到上限 %v, 实际 %v", maxBackoff, Backoff(1000))
	}
}

// TestBackoffHandlesNonPositiveRetry 验证非正数次重试返回基准退避。
//
// 首次失败时 retryCount 为 0（还未重试过），必须仍给出合理间隔；
// 负数属于异常输入，同样不能返回 0 或负数——那会导致立即重试打满 CPU。
func TestBackoffHandlesNonPositiveRetry(t *testing.T) {
	for _, i := range []int{0, -1, -100} {
		if d := Backoff(i); d != baseBackoff {
			t.Errorf("retryCount=%d 时退避 = %v, 期望 %v", i, d, baseBackoff)
		}
	}
}

// TestBackoffNoOverflowOnLargeRetry 验证超大重试次数不产生溢出。
//
// 位移次数过多会让 int64 溢出成负数，负退避会让重试立即发生，
// 与「退避」的意图完全相反，因此必须显式设限。
func TestBackoffNoOverflowOnLargeRetry(t *testing.T) {
	for _, i := range []int{62, 63, 64, 100, 1 << 20} {
		d := Backoff(i)
		if d <= 0 {
			t.Fatalf("retryCount=%d 产生非正退避: %v", i, d)
		}
	}
}
