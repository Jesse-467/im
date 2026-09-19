package ws

import (
	"encoding/json"
	"sync"

	klog "github.com/go-kratos/kratos/v2/log"
)

// Registry 管理本节点上的全部连接。
//
// 只管理「本节点」的连接：跨节点的路由由 Presence（Redis）负责。
// 这样划分的原因是本地连接是进程内状态，查询必须极快且不需要网络；
// 而跨节点投递是低频动作，走 Redis 完全可以接受。
type Registry struct {
	// conns 是 userId -> 该用户在本节点上的全部连接。
	//
	// 用「一对多」而不是「一对一」：同一个用户可能同时在手机、平板、
	// 网页上登录，消息要投给全部端。若只保留最后一条，先登录的端会静默收不到消息。
	conns map[int64]map[string]*Conn

	// nodeID 是本节点标识，用于跨节点路由与消费者组命名
	nodeID string

	mu  sync.RWMutex
	log *klog.Helper
}

// NewRegistry 构造连接注册中心。
func NewRegistry(presence *Presence, logger klog.Logger) *Registry {
	return &Registry{
		conns:  make(map[int64]map[string]*Conn),
		nodeID: presence.NodeID(),
		log:    klog.NewHelper(klog.With(logger, "module", "ws/registry")),
	}
}

// NodeID 返回本节点标识。
func (r *Registry) NodeID() string { return r.nodeID }

// Add 注册一条连接。
func (r *Registry) Add(c *Conn) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.conns[c.UserID] == nil {
		r.conns[c.UserID] = make(map[string]*Conn)
	}
	r.conns[c.UserID][c.ConnID] = c

	r.log.Infow("msg", "连接已注册",
		"uid", c.UserID, "connId", c.ConnID, "platform", c.Platform,
		"uidConns", len(r.conns[c.UserID]))
}

// Remove 注销一条连接。
//
// 同时负责把连接从索引中摘除并在用户无任何连接时清理空 map，
// 否则长期运行后 map 中会残留大量空条目造成内存泄漏。
func (r *Registry) Remove(c *Conn) {
	r.mu.Lock()
	defer r.mu.Unlock()

	byUser, ok := r.conns[c.UserID]
	if !ok {
		return
	}
	delete(byUser, c.ConnID)
	if len(byUser) == 0 {
		delete(r.conns, c.UserID)
	}

	r.log.Infow("msg", "连接已注销",
		"uid", c.UserID, "connId", c.ConnID, "remainConns", len(byUser))
}

// LocalConns 返回指定用户在本节点上的全部连接。
func (r *Registry) LocalConns(userID int64) []*Conn {
	r.mu.RLock()
	defer r.mu.RUnlock()

	byUser, ok := r.conns[userID]
	if !ok {
		return nil
	}

	out := make([]*Conn, 0, len(byUser))
	for _, c := range byUser {
		out = append(out, c)
	}
	return out
}

// IsOnlineLocally 判断用户在本节点上是否有活跃连接。
func (r *Registry) IsOnlineLocally(userID int64) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.conns[userID]) > 0
}

// Deliver 向本节点上的指定用户投递消息。
//
// 返回投递成功的连接数。调用方据此判断「本地是否有人收到」，
// 若为 0 则需要走跨节点路由。
//
// 队列满的连接会被主动关闭：它已无法消费消息，保留它只会持续占用内存，
// 且会误导在线状态。客户端重连后可通过 seq 补洞拿回缺失消息。
func (r *Registry) Deliver(userID int64, payload []byte) int {
	conns := r.LocalConns(userID)
	if len(conns) == 0 {
		return 0
	}

	delivered := 0
	for _, c := range conns {
		if c.trySend(payload) {
			delivered++
			continue
		}
		r.log.Warnw("msg", "连接发送队列已满或已关闭，主动断开",
			"uid", userID, "connId", c.ConnID)
		c.Close()
	}
	return delivered
}

// PushToUser 向指定用户在本节点的全部连接推送一条结构化消息。
//
// 实现 service.Pusher 接口。序列化集中在此处：调用方只提供结构体，
// 避免每个调用点各自 json.Marshal 造成编码不一致。
func (r *Registry) PushToUser(userID int64, msg Message) {
	payload, err := json.Marshal(msg)
	if err != nil {
		r.log.Errorw("msg", "序列化下行消息失败", "uid", userID, "err", err)
		return
	}
	r.Deliver(userID, payload)
}

// Broadcast 向本节点上的多个用户投递同一条消息。
//
// 返回实际送达的用户数，用于统计与告警。群聊场景下这是主要投递路径。
func (r *Registry) Broadcast(userIDs []int64, payload []byte) int {
	total := 0
	for _, uid := range userIDs {
		total += r.Deliver(uid, payload)
	}
	return total
}

// OnlineUserIDs 返回本节点上当前在线的全部用户 ID。
//
// 供节点退出时批量清理在线状态使用。
func (r *Registry) OnlineUserIDs() []int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ids := make([]int64, 0, len(r.conns))
	for uid := range r.conns {
		ids = append(ids, uid)
	}
	return ids
}

// ConnCount 返回本节点当前连接总数，供监控指标使用。
func (r *Registry) ConnCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	total := 0
	for _, byUser := range r.conns {
		total += len(byUser)
	}
	return total
}

// CloseAll 关闭本节点全部连接，用于优雅退出。
//
// 刻意不等待客户端确认：退出流程有时间上限，等待会拖长停机时间。
// 客户端感知到连接断开后会自行重连，并通过 seq 补齐消息。
func (r *Registry) CloseAll() {
	r.mu.Lock()
	defer r.mu.Unlock()

	n := 0
	for _, byUser := range r.conns {
		for _, c := range byUser {
			c.Close()
			n++
		}
	}
	r.conns = make(map[int64]map[string]*Conn)

	if n > 0 {
		r.log.Infow("msg", "已关闭本节点全部连接", "count", n)
	}
}
