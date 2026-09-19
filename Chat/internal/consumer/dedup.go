package consumer

import (
	"strconv"
	"sync"
)

// msgKey 生成消息的唯一键。
//
// 用「会话 + 序号」而不是消息 ID：seq 在会话内唯一且单调，
// 是消息顺序的权威标识；而消息 ID 是雪花值，虽然也唯一，
// 但用它会让去重窗口无法表达「这个会话我推到哪个位置了」。
func msgKey(conversationID, seq int64) string {
	var buf [40]byte
	b := strconv.AppendInt(buf[:0], conversationID, 10)
	b = append(b, ':')
	b = strconv.AppendInt(b, seq, 10)
	return string(b)
}

// dedupCache 是带容量上限的消息去重窗口。
//
// 为什么需要它：投递语义是 at-least-once，同一条消息可能被投递两次
// （Outbox 确认失败后回收重投、或 Kafka 自身的重复投递）。
// 客户端虽然也能按 (会话, seq) 去重，但让服务端不重复推送更省带宽，
// 也避免客户端因重复渲染而闪烁。
//
// 用 map + 环形淘汰而不是真正的 LRU：这里只需要「近期见过的不再处理」，
// 不需要精确的访问序。环形淘汰的代价是 O(1) 且无需维护链表，
// 而误淘汰一条去重记录的最坏后果只是多推一次——客户端仍会去重。
type dedupCache struct {
	mu   sync.Mutex
	seen map[string]struct{}
	// ring 记录写入顺序，用于在超容量时淘汰最老的键
	ring []string
	// next 是环形写入位置
	next int
	// cap 是容量上限
	cap int
}

// newDedupCache 构造去重窗口。capacity <= 0 时使用一个安全的最小值。
func newDedupCache(capacity int) *dedupCache {
	if capacity <= 0 {
		capacity = 1024
	}
	return &dedupCache{
		seen: make(map[string]struct{}, capacity),
		ring: make([]string, 0, capacity),
		cap:  capacity,
	}
}

// enter 尝试登记一个键。
//
// 返回 true 表示「首次见到，应当处理」；false 表示「近期已处理过，应跳过」。
// 注意：进入与离开必须成对使用——处理失败时调用 leave 放行，
// 否则一次失败就会让该消息永远无法被重推。
func (d *dedupCache) enter(key string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, ok := d.seen[key]; ok {
		return false
	}
	d.seen[key] = struct{}{}

	if len(d.ring) < d.cap {
		d.ring = append(d.ring, key)
		return true
	}

	// 容量已满：环形覆盖最老的键
	old := d.ring[d.next]
	delete(d.seen, old)
	d.ring[d.next] = key
	d.next = (d.next + 1) % d.cap
	return true
}

// leave 撤销一次 enter，使该键可以再次被处理。
//
// 仅在处理失败时调用：让重试仍然有效。
func (d *dedupCache) leave(key string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, ok := d.seen[key]; !ok {
		return
	}
	delete(d.seen, key)

	// 从环形里摘除：遍历一次的代价可接受，因为失败是低频路径。
	// 若不做这一步，该键会在被淘汰前一直占位，而 map 里已无记录，
	// 导致同一个键被重复插入环形数组、进而挤掉其他有效记录。
	for i, k := range d.ring {
		if k == key {
			d.ring = append(d.ring[:i], d.ring[i+1:]...)
			if d.next > i {
				d.next--
			}
			if d.next >= len(d.ring) {
				d.next = 0
			}
			break
		}
	}
}

// size 返回当前记录数，供测试与监控使用。
func (d *dedupCache) size() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.seen)
}
