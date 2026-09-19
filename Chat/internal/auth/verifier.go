// Package auth 提供聊天服务的统一令牌校验。
//
// 为什么需要单独一层而不是各处直接调 auth.Parse：
// 校验实际上有两步——本地验签 + （可选的）向账号中心确认未被吊销。
// 这两步必须在 HTTP 中间件与 WebSocket 网关之间保持一致，
// 否则会出现「HTTP 拒绝了但 WS 放行」这类难以排查的不一致。
package auth

import (
	"context"
	"errors"
	"fmt"

	klog "github.com/go-kratos/kratos/v2/log"
)

// ErrTokenRevoked 表示账号中心确认该令牌已被吊销（登出 / 被踢 / 改密）。
//
// 与本地校验失败区分开：本地失败是「签名错或过期」，本错误是
// 「令牌本身合法，但已被主动作废」。两者对外都返回 401，
// 但日志与监控需要能分开看——后者突增意味着踢下线策略在生效。
var ErrTokenRevoked = errors.New("令牌已被吊销")

// Verifier 是令牌校验的统一入口。
type Verifier struct {
	secret JWTSecret
	// remote 为 nil 时只做本地校验（等价于账号中心的 self 模式）
	remote TokenVerifier
	log    *klog.Helper
}

// TokenVerifier 向账号中心确认令牌状态。
type TokenVerifier interface {
	// VerifyToken 返回 (valid, userID, err)。
	// valid=false 且 err=nil 表示令牌不该被接受（业务分支）；
	// err != nil 表示无法确认（服务端故障），调用方必须 fail-closed。
	VerifyToken(ctx context.Context, accessToken string) (bool, int64, error)
}

// NewVerifier 构造校验器。
//
// remote 为 nil 表示关闭远程校验（账号中心跑在 self 模式时的合理配置），
// 此时行为与改造前完全一致。
func NewVerifier(secret JWTSecret, remote TokenVerifier, logger klog.Logger) *Verifier {
	return &Verifier{
		secret: secret,
		remote: remote,
		log:    klog.NewHelper(klog.With(logger, "module", "auth/verifier")),
	}
}

// Remote 返回底层远程校验器，供需要在连接存活期间复核令牌状态的场景使用
// （如 WebSocket 网关的周期性踢下线检查）。
//
// 自校验模式下返回 nil，调用方需判空：nil 表示没有可复核的远程来源，
// 此时应跳过复核而不是报错。
func (v *Verifier) Remote() TokenVerifier {
	return v.remote
}

// Verify 校验令牌并返回用户 ID。
//
// 执行顺序刻意是「先本地、再远程」：
//  1. 本地验签能挡掉绝大多数非法请求（伪造、过期），且零网络开销。
//     先做它可以让无效令牌不产生任何跨服务调用，避免被刷爆账号中心；
//  2. 本地通过后再向账号中心确认未被吊销。
//
// 错误语义（调用方据此决定对外响应）：
//   - ErrMissingToken / ErrInvalidToken → 401，客户端重新登录；
//   - ErrTokenRevoked                   → 401，令牌已被主动作废；
//   - 其他 error                        → 503/500，账号中心不可达，fail-closed。
func (v *Verifier) Verify(ctx context.Context, tokenString string) (int64, error) {
	uid, _, err := Parse(string(v.secret), tokenString)
	if err != nil {
		// 本地校验失败属于正常业务分支，不打日志以免被无效令牌刷屏
		return 0, err
	}

	if v.remote == nil {
		return uid, nil
	}

	valid, remoteUID, err := v.remote.VerifyToken(ctx, tokenString)
	if err != nil {
		// 账号中心不可达：不能放行。这是安全底线——
		// 放行等于把「可能已被吊销」的令牌当成有效，被踢的设备会重新获得访问权。
		v.log.Errorw("msg", "向账号中心校验令牌失败，拒绝放行",
			"uid", uid, "err", err)
		return 0, fmt.Errorf("auth: 无法确认令牌状态: %w", err)
	}
	if !valid {
		v.log.Warnw("msg", "账号中心确认令牌已失效，拒绝放行", "uid", uid)
		return 0, ErrTokenRevoked
	}

	// 账号中心返回的 uid 与本地解析结果不一致，说明存在密钥或实现错乱，
	// 这属于严重异常（可能意味着签发与校验用了不同的密钥），必须拒绝。
	if remoteUID != uid {
		v.log.Errorw("msg", "账号中心返回的 uid 与本地解析不一致，拒绝放行",
			"localUid", uid, "remoteUid", remoteUID)
		return 0, ErrTokenRevoked
	}

	return uid, nil
}
