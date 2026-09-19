package ws

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	klog "github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/transport"
	"github.com/gorilla/websocket"

	"github.com/Jesse-467/im/Chat/internal/auth"
	"github.com/Jesse-467/im/Chat/internal/conf"
	"github.com/Jesse-467/im/Chat/internal/xid"
)

// 编译期断言：WS 网关必须满足 Kratos 的传输层契约。
//
// 满足该契约后，它能与 HTTP、gRPC 一起交给 kratos.App 统一管理启停，
// 优雅退出时连接会被有序关闭，而不是随进程被强杀。
var (
	_ transport.Server     = (*Server)(nil)
	_ transport.Endpointer = (*Server)(nil)
)

// 下行消息类型
const (
	// TypeMessage 新消息推送
	TypeMessage = "message"
	// TypePong 心跳应答
	TypePong = "pong"
	// TypeError 错误提示
	TypeError = "error"
)

// Server 是 WebSocket 网关。
type Server struct {
	cfg      *conf.Config
	registry *Registry
	presence *Presence
	upgrader websocket.Upgrader

	idGen *xid.Generator
	log   *klog.Helper

	// handler 处理客户端上行消息。
	// 由 service 层注入，网关本身不理解业务语义。
	handler UpstreamHandler

	addr string
}

// UpstreamHandler 处理客户端上行消息。
//
// 定义成函数类型而非接口：上行处理只有一个入口，用函数更直接，
// 也避免为它引入一个只有单实现的接口。
type UpstreamHandler func(ctx context.Context, userID int64, msg *UpstreamMessage) error

// UpstreamMessage 是客户端上行消息。
type UpstreamMessage struct {
	// Type 上行类型：send（发送消息）/ ping（心跳）
	Type string `json:"type"`
	// 以下字段用于发送消息
	ConversationID int64  `json:"conversationId"`
	GroupID        string `json:"groupId"`
	Content        string `json:"content"`
	MsgType        int32  `json:"msgType"`
	ClientMsgID    string `json:"clientMsgId"`
	Extra          string `json:"extra"`
}

// NewServer 构造 WebSocket 网关。
func NewServer(
	c *conf.Config,
	registry *Registry,
	presence *Presence,
	idGen *xid.Generator,
	handler UpstreamHandler,
	logger klog.Logger,
) *Server {
	return &Server{
		cfg:      c,
		registry: registry,
		presence: presence,
		idGen:    idGen,
		handler:  handler,
		addr:     c.App.WSAddr,
		log:      klog.NewHelper(klog.With(logger, "module", "ws/server")),
		upgrader: websocket.Upgrader{
			// 缓冲大小与连接层的常量对齐：过小会导致大消息被丢弃
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			// 允许所有来源：鉴权由 token 承担，Origin 校验在移动端与
			// 多域名场景下会误伤，需要按业务白名单时再单独开启。
			CheckOrigin: func(_ *http.Request) bool { return true },
		},
	}
}

// Handler 返回供 HTTP 引擎挂载的处理函数。
//
// 网关不自建 HTTP 服务，而是挂到已有的 Gin 引擎上：
// 这样 WS 与 HTTP 共用同一端口与同一套中间件，部署时只需暴露一个端口。
func (s *Server) Handler() gin.HandlerFunc {
	return func(c *gin.Context) {
		s.serve(c.Writer, c.Request)
	}
}

// Start 满足 transport.Server 契约。
//
// 本网关不独立监听端口（挂载在 HTTP 引擎上），因此这里直接返回。
// 之所以仍然实现该接口：让 kratos.App 能感知它的存在，
// 从而在退出时调用 Stop 完成连接排空。
func (s *Server) Start(_ context.Context) error {
	s.log.Infow("msg", "WebSocket 网关已就绪", "path", s.cfg.App.WSPath, "nodeId", s.presence.NodeID())
	return nil
}

// Stop 关闭全部连接。
//
// 在传输层关闭前执行：先断开长连接，再停 HTTP 服务，
// 保证客户端能立即感知并触发重连，而不是等到 TCP 超时。
func (s *Server) Stop(_ context.Context) error {
	s.registry.CloseAll()
	return nil
}

// Endpoint 返回 WS 地址，供服务注册使用。
func (s *Server) Endpoint() (*url.URL, error) {
	return url.Parse("ws://" + s.addr + s.cfg.App.WSPath)
}

// serve 处理一次连接升级与生命周期。
func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	// 鉴权必须在升级之前完成：升级后再校验的话，非法连接已经占用了
	// 一个 WebSocket 连接与对应资源，且客户端拿到的是「连接成功后立刻断开」
	// 这种难以处理的错误形态。
	uid, err := s.authenticate(r)
	if err != nil {
		s.log.Warnw("msg", "WebSocket 鉴权失败", "err", err, "remote", r.RemoteAddr)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.log.Warnw("msg", "WebSocket 升级失败", "uid", uid, "err", err)
		return
	}

	platform := r.Header.Get("platform")
	if platform == "" {
		platform = "unknown"
	}

	c := newConn(uid, s.presence.NodeID(), platform, strconv.FormatInt(s.idGen.MustNext(), 36))

	s.registry.Add(c)
	s.markOnline(r.Context(), uid)

	// 连接退出时统一做清理，保证任何异常路径都不会遗漏
	defer func() {
		c.Close()
		s.registry.Remove(c)
		s.markOffline(uid)
		_ = conn.Close()
	}()

	// 读写各自独立协程：读协程阻塞等客户端数据，写协程阻塞等下行队列，
	// 合并成一个协程会导致任一方阻塞时另一方也停摆。
	go s.writePump(conn, c)
	s.readPump(conn, c)
}

// authenticate 从查询参数或请求头中取出令牌并校验。
//
// 优先取查询参数：浏览器的 WebSocket API 无法自定义请求头，
// 只能把令牌放在 URL 上；移动端则可选地使用请求头。
func (s *Server) authenticate(r *http.Request) (int64, error) {
	token := r.URL.Query().Get("token")
	if token == "" {
		token = strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	}
	if token == "" {
		return 0, auth.ErrMissingToken
	}
	uid, _, err := auth.Parse(s.cfg.App.JWTSecret, token)
	if err != nil {
		return 0, err
	}
	return uid, nil
}

// readPump 读取客户端消息，阻塞直到连接关闭。
func (s *Server) readPump(conn *websocket.Conn, c *Conn) {
	conn.SetReadLimit(maxMessageSize)
	_ = conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		// 收到 pong 说明连接仍然健康：刷新活跃时间并延后读超时。
		// 这是判断"连接是否已死"的唯一可靠依据——TCP 层在客户端
		// 异常断电等场景下不会主动通知服务端。
		c.touch()
		return conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			s.log.Infow("msg", "WebSocket 连接读取结束", "uid", c.UserID, "connId", c.ConnID, "err", err)
			return
		}

		c.touch()

		var up UpstreamMessage
		if err := json.Unmarshal(data, &up); err != nil {
			s.sendError(c, "消息格式不正确")
			continue
		}

		// ping 在网关层直接应答，不进入业务层：
		// 心跳是传输层关注点，让它穿过业务层只会增加无谓开销。
		if up.Type == "ping" {
			s.send(c, Message{Type: TypePong, Data: time.Now().UnixMilli()})
			continue
		}

		if s.handler != nil {
			if err := s.handler(context.Background(), c.UserID, &up); err != nil {
				s.log.Warnw("msg", "处理上行消息失败", "uid", c.UserID, "type", up.Type, "err", err)
				s.sendError(c, err.Error())
			}
		}
	}
}

// writePump 消费发送队列，是唯一的写协程。
func (s *Server) writePump(conn *websocket.Conn, c *Conn) {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	for {
		select {
		case payload := <-c.send:
			_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
				s.log.Infow("msg", "WebSocket 写入失败", "uid", c.UserID, "connId", c.ConnID, "err", err)
				c.Close()
				return
			}

		case <-ticker.C:
			// 定期发 ping：客户端回复 pong 才会刷新读超时，
			// 因此如果客户端已死，读协程会因超时而退出并触发清理。
			_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				c.Close()
				return
			}

		case <-c.closed:
			// 连接被要求关闭：发一条关闭帧让客户端立即感知，
			// 而不是等到 TCP 超时（默认可能长达数分钟）。
			_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
			_ = conn.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			return
		}
	}
}

// send 把消息投递到连接的发送队列。
func (s *Server) send(c *Conn, msg Message) {
	payload, err := json.Marshal(msg)
	if err != nil {
		s.log.Errorw("msg", "序列化下行消息失败", "err", err)
		return
	}
	if !c.trySend(payload) {
		s.log.Warnw("msg", "下行队列已满，丢弃消息", "uid", c.UserID, "connId", c.ConnID)
	}
}

// sendError 向客户端发送错误提示。
func (s *Server) sendError(c *Conn, msg string) {
	s.send(c, Message{Type: TypeError, Data: msg})
}

// markOnline 登记在线状态。
func (s *Server) markOnline(ctx context.Context, uid int64) {
	if err := s.presence.Online(ctx, uid); err != nil {
		// 在线状态登记失败不应影响连接建立：连接的可用性优先。
		// 降级表现是该用户的消息可能不会被推送到本节点，
		// 但客户端仍可通过拉取接口拿到消息。
		s.log.Errorw("msg", "登记在线状态失败", "uid", uid, "err", err)
	}
}

// markOffline 清除在线状态。
func (s *Server) markOffline(uid int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := s.presence.Offline(ctx, uid); err != nil {
		s.log.Errorw("msg", "清除在线状态失败", "uid", uid, "err", err)
	}
}
