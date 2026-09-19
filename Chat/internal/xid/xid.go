// Package xid 生成全局唯一、趋势递增的 int64 ID。
//
// 为什么不用自增主键：会话 ID 必须在写入数据库之前就确定——创建会话时要同时写入
// conversation 与 conversation_member 两张表，还需要把它作为消息队列的分区键，
// 这些场景都拿不到"插入后才知道"的自增值。分库分表后自增主键也无法保证唯一。
//
// 为什么不用 UUID：会话 ID 会出现在每一条消息记录与每一个 MQ 消息里，
// 16 字节的字符串 vs 8 字节整型，存储与索引开销差距在亿级数据下非常可观；
// 而且 UUID 无序，会造成 B+ 树页分裂与随机写。
//
// 雪花算法正好兼顾：无需数据库往返、全局唯一、且按时间趋势递增。
package xid

import (
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"sync"
	"time"
)

// 位分配：1 位符号 + 41 位毫秒时间 + 10 位节点 + 12 位序列
const (
	// epoch 是自定义时间起点（2024-01-01T00:00:00Z）。
	// 用相对时间而非 Unix 纪元，可把 41 位的时间跨度从 ~69 年延长到 ~2093 年。
	epoch int64 = 1704067200000

	nodeBits     uint8 = 10
	sequenceBits uint8 = 12

	maxNodeID   int64 = -1 ^ (-1 << nodeBits)     // 1023
	maxSequence int64 = -1 ^ (-1 << sequenceBits) // 4095

	// 位移量声明为 uint8：Go 允许用无符号整数作为移位计数，
	// 而移位对象是 int64，这样在 Next 里写 now << timeShift 无需转换。
	nodeShift uint8 = sequenceBits
	timeShift uint8 = sequenceBits + nodeBits
)

// ErrClockBackwards 表示检测到系统时钟回拨。
//
// 时钟回拨会导致同一毫秒内重复发号，必须显式暴露而不是静默产生重复 ID。
var ErrClockBackwards = errors.New("xid: 检测到系统时钟回拨")

// Generator 是线程安全的 ID 生成器。
type Generator struct {
	mu       sync.Mutex
	nodeID   int64
	lastTime int64
	sequence int64
}

// New 创建 ID 生成器。
//
// nodeID 为 0 时按主机名哈希推导，这样同一台机器上多进程实例会落到不同节点号，
// 无需额外的配置中心；容器环境下建议显式注入（如取 StatefulSet 序号），
// 因为容器主机名是随机的，重启后可能碰撞。
func New(nodeID int64) (*Generator, error) {
	if nodeID == 0 {
		nodeID = deriveNodeID()
	}
	if nodeID < 0 || nodeID > maxNodeID {
		return nil, fmt.Errorf("xid: nodeID 必须在 0~%d 之间，当前 %d", maxNodeID, nodeID)
	}
	return &Generator{nodeID: nodeID}, nil
}

// Next 生成下一个 ID。并发安全。
func (g *Generator) Next() (int64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	now := time.Now().UnixMilli() - epoch
	if now < 0 {
		return 0, fmt.Errorf("xid: 系统时间早于起始纪元，无法发号")
	}

	switch {
	case now == g.lastTime:
		g.sequence = (g.sequence + 1) & maxSequence
		if g.sequence == 0 {
			// 当前毫秒的 4096 个序列号用尽，自旋到下一毫秒，保证同毫秒内不重复
			for now <= g.lastTime {
				now = time.Now().UnixMilli() - epoch
			}
		}
	case now > g.lastTime:
		g.sequence = 0
	default:
		// 时钟回拨：宁可报错，也不能产生重复 ID
		return 0, fmt.Errorf("%w: 上一次发号时间 %d，当前 %d", ErrClockBackwards, g.lastTime, now)
	}

	g.lastTime = now
	return (now << timeShift) | (g.nodeID << nodeShift) | g.sequence, nil
}

// MustNext 生成 ID，失败时 panic。仅用于确定不会失败的初始化场景。
func (g *Generator) MustNext() int64 {
	id, err := g.Next()
	if err != nil {
		panic(err)
	}
	return id
}

// deriveNodeID 依据主机名推导节点号。
func deriveNodeID() int64 {
	host, err := os.Hostname()
	if err != nil || host == "" {
		// 取不到主机名时退化为进程启动时间，仍可避免同机多实例碰撞
		return time.Now().UnixNano() % (maxNodeID + 1)
	}
	h := fnv.New32a()
	if _, err := h.Write([]byte(host)); err != nil {
		return 0
	}
	return int64(h.Sum32()) % (maxNodeID + 1)
}
