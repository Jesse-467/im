package ws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

	// TypeKicked 通知客户端「你的连接被服务端主动断开」（被踢下线 / 登出 / 改密）。
	//
	// 与 TypeError 区分开：错误提示是「这条上行消息有问题」，连接仍可继续；
	// 本类型是「连接即将关闭」，客户端收到后应停止重连并回到登录态。
	TypeKicked = "kicked"
)

// reverifyInterval 是连接存活期间向账号中心复核令牌状态的间隔。
//
// 握手时鉴权一次无法感知后续的令牌吊销（被踢下线 / 登出 / 改密），
// 因此网关按该间隔复核，发现被吊销即主动断开连接。
// 取值与心跳同量级：足够快让踢下线在短时间内生效，
// 又不至于给账号中心带来可观的额外调用量（每连接每分钟约 2 次）。
const reverifyInterval = 30 * time.Second

// Server 是 WebSocket 网关。
type Server struct {
	cfg      *conf.Config
	registry *Registry
	presence *Presence
	upgrader websocket.Upgrader

	// verifier 负责令牌校验：本地验签 + （按配置）向账号中心确认未被吊销。
	// 网关不自己解析令牌，避免 HTTP 与 WS 两条入口的鉴权语义出现偏差。
	verifier *auth.Verifier

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
//
// 会话标识刻意设计成「数字优先、字符串兜底」的双字段：
// conversationId 是 19 位雪花 ID，超出 JavaScript 的 Number.MAX_SAFE_INTEGER，
// 浏览器端用 JSON.parse 读服务端下发的 ID 时会被静默舍入，再回传就成了另一个
// 不存在的会话。因此客户端可以把 ID 当字符串原样回填到 conversationId 字段
// （JSON 里数字与字符串是不同的字面量，故需要独立字段承载），服务端两种都收。
type UpstreamMessage struct {
	// Type 上行类型：send（发送消息）/ ping（心跳）
	Type string `json:"type"`
	// ConversationID 数字形式的会话 ID
	ConversationID int64 `json:"conversationId"`
	// ConversationIDStr 字符串形式的会话 ID，用于规避前端浮点精度丢失。
	//
	// 与 GroupID 的区别：GroupID 是"按业务规则解析会话"的历史入口
	// （如 minUid_maxUid），而本字段是"精确的会话主键"，直接使用不做事后解析。
	ConversationIDStr string `json:"conversationIdStr"`
	GroupID           string `json:"groupId"`
	Content           string `json:"content"`
	MsgType           int32  `json:"msgType"`
	ClientMsgID       string `json:"clientMsgId"`
	Extra             string `json:"extra"`
}

// ResolveConversationID 返回本次上行消息的目标会话 ID（数字形式，0 表示未提供）。
//
// 同时接受 conversationId 的字符串字面量：部分 JSON 序列化实现会把大整数
// 编码为字符串（Go 侧解析到字符串字段同样能得到精确值），这不是错误格式，
// 因此不做报错处理。
func (m *UpstreamMessage) ResolveConversationID() (int64, error) {
	if m.ConversationID > 0 {
		return m.ConversationID, nil
	}
	if m.ConversationIDStr != "" {
		id, err := strconv.ParseInt(strings.TrimSpace(m.ConversationIDStr), 10, 64)
		if err != nil || id <= 0 {
			return 0, fmt.Errorf("conversationIdStr 不是合法的会话 ID: %q", m.ConversationIDStr)
		}
		return id, nil
	}
	return 0, nil
}

// UnmarshalJSON 兼容 conversationId 的两种编码。
//
// 标准解码只接受数字，但把大整数写成字符串是业界常见做法（许多网关与
// 序列化库默认如此）。若不兼容，客户端会收到"消息格式不正确"这种
// 指向不明的报错，且问题只在 ID 超过 2^53 时才会出现，极难定位。
func (m *UpstreamMessage) UnmarshalJSON(data []byte) error {
	// 用别名类型避免递归调用本方法
	type alias UpstreamMessage
	var raw struct {
		alias
		ConversationID json.RawMessage `json:"conversationId"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*m = UpstreamMessage(raw.alias)

	if len(raw.ConversationID) == 0 || string(raw.ConversationID) == "null" {
		return nil
	}
	// 数字形式：直接交给标准解码
	if err := json.Unmarshal(raw.ConversationID, &m.ConversationID); err == nil {
		return nil
	}
	// 字符串形式：去掉引号后按十进制解析
	var s string
	if err := json.Unmarshal(raw.ConversationID, &s); err != nil {
		return fmt.Errorf("conversationId 既不是数字也不是字符串: %s", raw.ConversationID)
	}
	m.ConversationIDStr = s
	return nil
}

// NewServer 构造 WebSocket 网关。
func NewServer(
	c *conf.Config,
	registry *Registry,
	presence *Presence,
	verifier *auth.Verifier,
	idGen *xid.Generator,
	handler UpstreamHandler,
	logger klog.Logger,
) *Server {
	return &Server{
		cfg:      c,
		registry: registry,
		presence: presence,
		verifier: verifier,
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
	//
	// 鉴权可能包含一次跨服务调用（向账号中心确认令牌未被吊销），
	// 因此显式设置超时：不能让建立连接的过程被下游卡住。
	authCtx, cancel := context.WithTimeout(r.Context(), authTimeout)
	defer cancel()

	uid, token, err := s.authenticate(authCtx, r)
	if err != nil {
		s.log.Warnw("msg", "WebSocket 鉴权失败", "err", err, "remote", r.RemoteAddr)
		// 区分「凭证被吊销」与「令牌无效/过期」：浏览器 WebSocket API 读不到
		// 响应体，但移动端与自定义客户端可以据此决定是刷新令牌还是直接重新登录。
		if errors.Is(err, auth.ErrTokenRevoked) {
			http.Error(w, "token revoked", http.StatusForbidden)
		} else {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		}
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

	c := newConn(uid, s.presence.NodeID(), platform, strconv.FormatInt(s.idGen.MustNext(), 36), token)

	s.registry.Add(c)
	s.markOnline(r.Context(), uid)

	s.log.Infow("msg", "WebSocket 连接已建立",
		"uid", uid, "connId", c.ConnID, "platform", platform,
		"remote", r.RemoteAddr, "localConns", s.registry.ConnCount())

	// 连接退出时统一做清理，保证任何异常路径都不会遗漏
	defer func() {
		c.Close()
		s.registry.Remove(c)
		s.markOffline(uid)
		_ = conn.Close()
		s.log.Infow("msg", "WebSocket 连接已断开",
			"uid", uid, "connId", c.ConnID, "platform", platform,
			"durationMs", time.Since(c.LastActive()).Milliseconds())
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
//
// 校验委托给 auth.Verifier：它在本地验签之外还会（按配置）向账号中心
// 确认令牌未被吊销，因此被踢下线的设备无法再建立长连接。
//
// 返回原始令牌是为了让网关在连接存活期间能周期性复核其状态
// （见 writePump 的 reverify 逻辑）。
func (s *Server) authenticate(ctx context.Context, r *http.Request) (int64, string, error) {
	token := r.URL.Query().Get("token")
	if token == "" {
		token = strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	}
	if token == "" {
		return 0, "", auth.ErrMissingToken
	}
	uid, err := s.verifier.Verify(ctx, token)
	if err != nil {
		return 0, "", err
	}
	return uid, token, nil
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

	reverify := time.NewTicker(reverifyInterval)
	defer reverify.Stop()

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

		case <-reverify.C:
			// 周期性向账号中心复核令牌状态：握手时的鉴权无法感知后续的
			// 吊销（被踢下线 / 登出 / 改密），不复核的话被踢设备的连接会
			// 一直存活到自然断开，仍能收到消息。
			if s.tokenStillValid(c) {
				continue
			}
			s.log.Infow("msg", "令牌已被吊销，主动断开连接",
				"uid", c.UserID, "connId", c.ConnID, "platform", c.Platform)
			// 这里不能调用 send：send 只是把帧放入 c.send，而紧接着 Close
			// 会让 writePump 先选中 c.closed，导致 kicked 帧丢失。直接在唯一
			// 写协程中写出通知，再发关闭帧，客户端才能停止重连并回到登录态。
			s.writeAndClose(conn, c, Message{Type: TypeKicked, Data: "登录状态已失效，请重新登录"})
			c.Close()
			return

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

// tokenStillValid 向账号中心复核连接令牌是否仍然有效。
//
// 账号中心不可达时按「仍然有效」处理：这是刻意的可用性取舍——
// 账号中心短暂抖动不应导致全部在线连接被误踢。代价是抖动期间
// 踢下线动作会延迟到账号中心恢复后才生效。
func (s *Server) tokenStillValid(c *Conn) bool {
	remote := s.verifierRemote()
	if c.token == "" || remote == nil {
		// 自校验模式（无远程校验）下没有可复核的来源，跳过：
		// 此时踢下线本就依赖客户端下次握手时被拒，连接级复核无从谈起。
		return true
	}

	ctx, cancel := context.WithTimeout(context.Background(), authTimeout)
	defer cancel()

	valid, _, err := remote.VerifyToken(ctx, c.token)
	if err != nil {
		s.log.Warnw("msg", "复核令牌状态失败（账号中心不可达），暂不断开",
			"uid", c.UserID, "connId", c.ConnID, "err", err)
		return true
	}
	return valid
}

// verifierRemote 返回用于复核令牌状态的远程校验器。
//
// 通过 Verifier 暴露的接口访问，避免网关直接持有账号中心客户端。
func (s *Server) verifierRemote() auth.TokenVerifier {
	return s.verifier.Remote()
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

// writeAndClose 在 writePump 内同步写出最后一条控制消息，再发送正常关闭帧。
// 该方法只允许由 writePump 调用，避免违反 gorilla/websocket 的单写协程约束。
func (s *Server) writeAndClose(conn *websocket.Conn, c *Conn, msg Message) {
	payload, err := json.Marshal(msg)
	if err != nil {
		s.log.Errorw("msg", "序列化关闭通知失败", "uid", c.UserID, "err", err)
		return
	}
	_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
	if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		s.log.Infow("msg", "WebSocket 关闭通知写入失败", "uid", c.UserID, "connId", c.ConnID, "err", err)
		return
	}
	_ = conn.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "token revoked"))
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
