// Package ws 实现 WebSocket 长连接网关。
//
// 网关的职责边界：只负责「连接的生命周期」与「消息的下行投递」，
// 不负责业务规则。业务校验（是不是成员、消息合不合法）全部由 biz 层完成，
// 这样 HTTP 与 WebSocket 两条入口共享同一套规则，行为不会出现差异。
package ws

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 连接与缓冲的参数
const (
	// sendBufferSize 是单个连接的发送缓冲队列长度。
	//
	// 必须有缓冲：客户端网络抖动时，若让发送方直接阻塞在写 socket 上，
	// 一条慢连接会拖慢整个投递协程，进而影响同节点其他用户。
	// 缓冲满了说明该连接确实跟不上，此时主动断开比无限积压更合理。
	sendBufferSize = 256

	// writeWait 单条消息的写超时
	writeWait = 10 * time.Second
	// pongWait 等待客户端 pong 的最长时间。
	// 超过该时间未收到 pong 即认为连接已死。取值需大于 pingPeriod，
	// 否则会在客户端还未来得及回应时就误判掉线。
	pongWait = 60 * time.Second
	// pingPeriod 是服务端主动发 ping 的间隔，必须小于 pongWait
	pingPeriod = 50 * time.Second
	// maxMessageSize 限制客户端上行单条消息大小，防止超大帧耗尽内存
	maxMessageSize = 4096

	// authTimeout 是建立连接时鉴权的最长耗时。
	//
	// 鉴权可能包含一次向账号中心确认令牌状态的跨服务调用，
	// 必须有超时：否则账号中心卡住会让新连接全部挂起，
	// 表现为「服务还活着，但没人能连上」。
	// 取值偏短是因为这是用户可感知的握手阶段，宁可让客户端重试。
	authTimeout = 3 * time.Second
)

// Message 是下行投递的消息体。
//
// 用结构体而非裸字节切片：投递方（消息消费者）需要知道这条消息属于
// 哪个会话、哪个用户，才能决定投给谁；编码为 JSON 的动作交给连接层。
type Message struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

// ID 是下行消息中承载大整数标识的类型，序列化为 JSON 字符串。
//
// 会话 ID 与消息 ID 是 19 位雪花值，超出 JavaScript 的
// Number.MAX_SAFE_INTEGER。若按数字下发，浏览器 JSON.parse 会把它
// 静默舍入（...784 变成 ...800），客户端再把该值回传就指向了不存在的会话。
//
// 之所以在本包独立定义而不是复用 service 层的类型：ws 层被 consumer 与
// server 共同依赖，不应反向依赖 service。两个包各自的 ID 类型都只做
// 同一件事（int64 ↔ 字符串），保持独立比引入跨层依赖更简单。
type ID int64

// MarshalJSON 输出为 JSON 字符串。
func (i ID) MarshalJSON() ([]byte, error) {
	return []byte(`"` + strconv.FormatInt(int64(i), 10) + `"`), nil
}

// UnmarshalJSON 兼容字符串与数字两种字面量。
func (i *ID) UnmarshalJSON(data []byte) error {
	s := strings.TrimSpace(string(data))
	if s == "" || s == "null" {
		*i = 0
		return nil
	}
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
		if s == "" {
			*i = 0
			return nil
		}
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("ws: ID 必须是整数或整数字符串，收到 %s", data)
	}
	*i = ID(v)
	return nil
}

// Conn 表示一条已建立的连接。
type Conn struct {
	// UserID 连接所属用户
	UserID int64
	// NodeID 处理该连接的网关节点 ID，用于多节点路由
	NodeID string
	// Platform 客户端平台标识（如 web / ios / android），支持多端同时在线
	Platform string
	// ConnID 连接唯一标识，用于精确定位与踢下线
	ConnID string

	// send 是下行消息队列。
	// 所有下行都走这个队列，由唯一的写协程消费——
	// gorilla/websocket 不允许并发写，集中到一个协程是唯一安全的做法。
	send chan []byte

	// closeOnce 保证关闭动作只执行一次。
	// 多个触发源（读写协程出错、心跳超时、被踢下线）都可能触发关闭，
	// 重复关闭 channel 会 panic。
	closeOnce sync.Once
	closed    chan struct{}

	// lastActive 记录最后活跃时间。
	//
	// 由心跳与每次收发包刷新，是判断连接是否存活的依据。
	// 注意：这里是「连接」级别的活跃，与用户在线状态是两件事。
	lastActive atomicTime
}

// newConn 创建连接。
func newConn(userID int64, nodeID, platform, connID string) *Conn {
	c := &Conn{
		UserID:   userID,
		NodeID:   nodeID,
		Platform: platform,
		ConnID:   connID,
		send:     make(chan []byte, sendBufferSize),
		closed:   make(chan struct{}),
	}
	c.touch()
	return c
}

// touch 刷新活跃时间。
func (c *Conn) touch() { c.lastActive.Store(time.Now()) }

// LastActive 返回最后活跃时间。
func (c *Conn) LastActive() time.Time { return c.lastActive.Load() }

// Close 关闭连接，可重复调用。
func (c *Conn) Close() {
	c.closeOnce.Do(func() { close(c.closed) })
}

// Closed 返回连接是否已关闭。
func (c *Conn) Closed() bool {
	select {
	case <-c.closed:
		return true
	default:
		return false
	}
}

// trySend 尝试投递一条消息到发送队列。
//
// 返回 false 表示队列已满或连接已关闭，调用方据此决定是否放弃该连接。
// 这里刻意不阻塞：投递方是消息消费者，绝不能因为某条慢连接而卡住。
func (c *Conn) trySend(payload []byte) bool {
	if c.Closed() {
		return false
	}
	select {
	case c.send <- payload:
		return true
	case <-c.closed:
		return false
	default:
		// 队列满：该连接消费不过来，交由调用方处理（通常是断开）
		return false
	}
}

// atomicTime 是基于 Mutex 的时间封装。
//
// 没有用 atomic.Value 是因为它对 time.Time 的存取需要额外的类型断言与
// 接口装箱，而这里读写频率不高（每次心跳与收发包），互斥锁足够且更直观。
type atomicTime struct {
	mu sync.RWMutex
	t  time.Time
}

// Store 写入时间。
func (a *atomicTime) Store(t time.Time) {
	a.mu.Lock()
	a.t = t
	a.mu.Unlock()
}

// Load 读取时间。
func (a *atomicTime) Load() time.Time {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.t
}
