package biz

import (
	"context"
	"errors"
	"sort"
	"time"

	klog "github.com/go-kratos/kratos/v2/log"
)

// 令牌吊销原因。写入 account_token.revoked_by，供安全审计区分场景。
const (
	// RevokeReasonLogout 用户主动登出
	RevokeReasonLogout = "logout"
	// RevokeReasonEvicted 被新登录的设备挤出（超出设备数上限）
	RevokeReasonEvicted = "evicted"
	// RevokeReasonPasswordReset 改密导致全部旧令牌失效
	RevokeReasonPasswordReset = "password_reset"
)

// 领域错误。由 service 层翻译为对外错误码。
var (
	// ErrTokenRevoked 表示令牌存在但已被吊销（登出 / 被踢 / 改密）。
	//
	// 注意：VerifyToken 在校验到这种情况时返回 (Valid=false, err=nil)
	// 而不是本错误——「令牌失效」是正常业务分支，返回 error 会让
	// 调用方对外报 500，把一次正常的重新登录变成服务故障。
	// 本错误保留给需要显式区分失败原因的调用方（如后续的会话管理接口）。
	ErrTokenRevoked = errors.New("令牌已被吊销")
	// ErrTokenNotFound 表示令牌在存储中不存在，语义约定同 ErrTokenRevoked。
	ErrTokenNotFound = errors.New("令牌不存在")
	// ErrTokenBindingRequired 表示 db 模式下令牌缺少 jti，无法回查。
	ErrTokenBindingRequired = errors.New("令牌缺少 jti，无法在 db 模式下校验")
)

// AuthToken 是一条已签发令牌的持久化记录。
//
// 注意这里刻意不保存令牌原文：只保存 jti（JWT 的 jti claim）。
// 这样即使数据库被读取，也无法据此伪造或复用任何令牌。
type AuthToken struct {
	ID       int64
	UserID   int64
	JTI      string
	DeviceID string
	Platform string
	ExpireAt time.Time
	// RevokedAt 为空表示仍然有效
	RevokedAt *time.Time
	// RevokedBy 记录吊销原因（logout / evicted / password_reset）
	RevokedBy  string
	LastSeenAt time.Time
	CreatedAt  time.Time

	// Evicted 是内存态标记，不落库：本次签发过程中是否踢掉了别的设备。
	// 由 UseCase 回传给 service 层，用于告知客户端「你挤掉了谁」。
	Evicted bool
}

// IsValid 判断令牌在指定时刻是否仍然有效。
//
// 同时校验「未吊销」与「未过期」：过期时间虽然写在 JWT 里，
// 但数据库这行也可能先于 JWT 过期（例如管理员手工改过 expire_at），
// 因此两处都要判断，以更严格的一方为准。
func (t *AuthToken) IsValid(now time.Time) bool {
	return t.RevokedAt == nil && now.Before(t.ExpireAt)
}

// TokenRepo 是令牌仓储接口，由 data 层实现。
type TokenRepo interface {
	// Save 写入或更新一条令牌记录。
	//
	// 按 (user_id, device_id) 冲突更新：同一设备重新登录时复用同一行，
	// 否则同一台设备反复登录会把自己的在线位占满。
	// 返回值是落库后的记录（含自增 ID）。
	Save(ctx context.Context, token *AuthToken) error

	// FindByJTI 按 jti 查询令牌，不存在时返回 (nil, nil)。
	//
	// 返回 nil 而非错误：调用方用它表达「这个令牌不是本服务签发的
	// 或已被清理」，属于正常业务分支，不是故障。
	FindByJTI(ctx context.Context, jti string) (*AuthToken, error)

	// FindByDevice 按 (user_id, device_key) 查询令牌，不存在时返回 (nil, nil)。
	//
	// 供踢人使用：有序集合里存的是 device_key，需要据此反查出要吊销哪个 jti。
	// device_key 与行的对应关系是 DeviceKey(device_id, jti) 的约定，
	// 因此实现需按该约定匹配，而不是简单地比较 device_id 列。
	FindByDevice(ctx context.Context, userID int64, deviceKey string) (*AuthToken, error)

	// Revoke 吊销一条令牌。revokedAt 非空视为已吊销，因此重复调用是幂等的。
	Revoke(ctx context.Context, jti, reason string, revokedAt time.Time) error

	// RevokeAllByUser 吊销某用户的全部有效令牌，返回受影响条数。
	//
	// 供改密使用：密码变更后所有旧令牌都应立即失效，
	// 否则被盗令牌在自然过期前仍然可用。
	RevokeAllByUser(ctx context.Context, userID int64, reason string, revokedAt time.Time) (int, error)

	// Touch 刷新令牌的最后活跃时间。
	//
	// 只更新 last_seen_at，失败不影响校验结果，因此调用方可以忽略错误。
	Touch(ctx context.Context, jti string, seenAt time.Time) error
}

// DeviceStore 维护「谁在线」的有序集合。
//
// 抽成接口而不是直接用 Redis：本层不依赖任何具体中间件，
// 这样设备上限的淘汰规则可以脱离 Redis 做纯单元测试。
type DeviceStore interface {
	// Add 记录一台设备上线，score 为上线时间（Unix 毫秒）。
	Add(ctx context.Context, userID int64, deviceKey string, at time.Time) error

	// Oldest 返回上线时间最早的 n 台设备，按时间升序。
	Oldest(ctx context.Context, userID int64, n int) ([]string, error)

	// Keys 返回该用户当前记录的全部设备标识（顺序不保证）。
	Keys(ctx context.Context, userID int64) ([]string, error)

	// Remove 移除一台设备的上线标记。
	Remove(ctx context.Context, userID int64, deviceKey string) error

	// Clear 移除该用户的全部设备标记，供改密等「全端下线」场景使用。
	Clear(ctx context.Context, userID int64) error
}

// SessionOptions 是会话用例的配置。
//
// 之所以打包成一个结构体而不是三个裸参数（int / bool / time.Duration）：
// Wire 在编译期按类型匹配依赖，三个不同类型的裸参数无法与
// 其他组件可能需要的同名类型区分，也无法表达「这三个值的语义」。
// 用结构体后，装配代码只需提供一个 SessionOptions，语义明确且不会串味。
type SessionOptions struct {
	// MaxDevices 是单账号允许同时在线的设备数上限，0 表示不限制。
	MaxDevices int
	// TokenModeDB 为 true 时，校验令牌还需回查数据库确认未被吊销。
	TokenModeDB bool
	// DeviceTTL 是设备在线标记的兜底有效期，0 表示不设置。
	DeviceTTL time.Duration
}

// SessionUseCase 承载「登录会话」相关的用例：
// 令牌持久化、多设备在线上限、按模式校验令牌。
//
// 独立于 UserUseCase 的原因：用户用例只关心账号本身（注册、资料、密码），
// 而设备与会话是另一条正交的生命周期。混在一起会让 UserUseCase
// 同时依赖 TokenRepo 与 DeviceStore，单元测试要为无关路径准备一堆桩。
type SessionUseCase struct {
	tokens  TokenRepo
	devices DeviceStore
	log     *klog.Helper

	// maxDevices 是单账号允许同时在线的设备数上限，0 表示不限制。
	maxDevices int
	// tokenModeDB 为 true 时，校验令牌还需回查数据库确认未被吊销。
	tokenModeDB bool
	// deviceTTL 是设备在线标记的兜底有效期，0 表示不设置。
	deviceTTL time.Duration
}

// NewSessionUseCase 构造会话用例。
func NewSessionUseCase(
	tokens TokenRepo,
	devices DeviceStore,
	logger klog.Logger,
	opts SessionOptions,
) *SessionUseCase {
	maxDevices := opts.MaxDevices
	if maxDevices < 0 {
		maxDevices = 0
	}
	return &SessionUseCase{
		tokens:      tokens,
		devices:     devices,
		log:         klog.NewHelper(klog.With(logger, "module", "biz/session")),
		maxDevices:  maxDevices,
		tokenModeDB: opts.TokenModeDB,
		deviceTTL:   opts.DeviceTTL,
	}
}

// RegisterDevice 登记一次成功登录，并在超出设备上限时踢出最早的设备。
//
// 执行顺序刻意如此（先落库、再进有序集合、最后淘汰）：
//  1. 先写令牌表：它是「令牌是否有效」的唯一权威。若先写有序集合而落库失败，
//     会出现「设备显示在线但令牌根本不存在」的假在线；
//  2. 再写有序集合：记录上线时间，淘汰顺序完全依赖它；
//  3. 最后按数量淘汰：淘汰动作会产生新的吊销写操作，放在最后可避免
//     中途失败留下「已淘汰但未吊销」的不一致。
//
// 返回值中的 evicted 是被踢下线的设备标识列表（可能为空），
// 供 service 层通知 Chat 网关断开对应长连接。
func (uc *SessionUseCase) RegisterDevice(ctx context.Context, token *AuthToken) ([]string, error) {
	if token == nil || token.UserID <= 0 || token.JTI == "" {
		return nil, ErrInvalidParam
	}
	deviceKey := DeviceKey(token.DeviceID, token.JTI)

	if err := uc.tokens.Save(ctx, token); err != nil {
		uc.log.Errorw("msg", "持久化令牌失败",
			"uid", token.UserID, "jti", token.JTI, "deviceKey", deviceKey, "err", err)
		return nil, err
	}

	if err := uc.devices.Add(ctx, token.UserID, deviceKey, token.CreatedAt); err != nil {
		// 设备登记失败不应让登录失败：令牌已经可用，最坏情况只是
		// 这台设备不参与多设备计数（表现为上限偶发失效）。
		// 这是刻意的可用性优先取舍，因此记 Error 以便发现依赖异常。
		uc.log.Errorw("msg", "登记设备上线失败，本次不参与设备数上限",
			"uid", token.UserID, "deviceKey", deviceKey, "err", err)
		return nil, nil
	}

	if uc.maxDevices <= 0 {
		uc.log.Infow("msg", "设备已上线（未开启设备数上限）",
			"uid", token.UserID, "deviceKey", deviceKey, "platform", token.Platform)
		return nil, nil
	}

	evicted, err := uc.evictExcess(ctx, token.UserID)
	if err != nil {
		// 淘汰失败不回滚本次登录：新设备应当能正常使用，
		// 代价是短时间内设备数可能超限，由下次登录继续收敛。
		uc.log.Errorw("msg", "淘汰超额设备失败，设备数可能暂时超限",
			"uid", token.UserID, "max", uc.maxDevices, "err", err)
		return nil, nil
	}

	uc.log.Infow("msg", "设备已上线",
		"uid", token.UserID, "deviceKey", deviceKey, "platform", token.Platform,
		"max", uc.maxDevices, "evicted", len(evicted))
	return evicted, nil
}

// evictExcess 把超出上限的设备逐出，并吊销它们的令牌。
//
// 每次都重新读取有序集合的规模，而不是用「1 + 上次的规模」推算：
// 并发登录（用户在多个端同时点登录）时，两个请求都会看到超限，
// 按实际规模淘汰能让后到的那次多踢一台，最终收敛到上限。
func (uc *SessionUseCase) evictExcess(ctx context.Context, userID int64) ([]string, error) {
	keys, err := uc.devices.Keys(ctx, userID)
	if err != nil {
		return nil, err
	}
	excess := len(keys) - uc.maxDevices
	if excess <= 0 {
		return nil, nil
	}

	// 按上线时间升序取最早的 excess 台——这正是「最先加入有序集合」的语义
	oldest, err := uc.devices.Oldest(ctx, userID, excess)
	if err != nil {
		return nil, err
	}
	if len(oldest) == 0 {
		return nil, nil
	}

	evicted := make([]string, 0, len(oldest))
	now := time.Now()
	for _, deviceKey := range oldest {
		// 从有序集合移除：让这台设备不再占用名额
		if err := uc.devices.Remove(ctx, userID, deviceKey); err != nil {
			uc.log.Errorw("msg", "移除设备在线标记失败",
				"uid", userID, "deviceKey", deviceKey, "err", err)
			continue
		}

		// 吊销其令牌。这里按 deviceKey 反查 jti，而不是把 jti 直接编码进
		// deviceKey：deviceKey 是客户端可见的标识，不应携带可用于
		// 定位令牌的内部 ID。
		tok, err := uc.tokens.FindByDevice(ctx, userID, deviceKey)
		if err != nil {
			uc.log.Errorw("msg", "查询待踢出设备的令牌失败",
				"uid", userID, "deviceKey", deviceKey, "err", err)
			continue
		}
		if tok == nil {
			// 设备标记存在但令牌已不在（例如被清理过），属于残留脏数据，
			// 移除标记即可，不需要吊销。
			uc.log.Warnw("msg", "设备标记存在但令牌缺失，按残留标记清理",
				"uid", userID, "deviceKey", deviceKey)
			continue
		}

		if err := uc.tokens.Revoke(ctx, tok.JTI, RevokeReasonEvicted, now); err != nil {
			uc.log.Errorw("msg", "吊销被踢设备的令牌失败",
				"uid", userID, "deviceKey", deviceKey, "jti", tok.JTI, "err", err)
			continue
		}

		uc.log.Infow("msg", "设备超出上限已被踢出",
			"uid", userID, "deviceKey", deviceKey, "platform", tok.Platform,
			"jti", tok.JTI, "reason", RevokeReasonEvicted)
		evicted = append(evicted, deviceKey)
	}

	return evicted, nil
}

// VerifyResult 是令牌校验结果。
type VerifyResult struct {
	Valid    bool
	UserID   int64
	ExpireAt int64
	// JTI 仅在令牌可解析时返回
	JTI string
}

// VerifyToken 按配置的模式校验令牌。
//
//   - self 模式：只要 JWT 签名正确且在有效期内即通过，不查存储。
//   - db 模式：在 JWT 自校验之上，还要求数据库中该 jti 存在且未被吊销。
//
// 返回值的约定（调用方据此决定对外语义，务必保持）：
//   - (Valid=false, err=nil)  → 令牌本身不该被接受。属于**正常业务分支**，
//     对应 HTTP 401，客户端重新登录即可。
//   - (Valid=false, err≠nil)  → **存储故障**导致无法判定。属于服务端故障，
//     对应 HTTP 500，需要告警与重试。
//
// 这个区分是刻意的：把「令牌被吊销」也当成 error 返回的话，调用方
// 按 err != nil 处理会对外报 500，把一次正常的重新登录变成服务故障。
func (uc *SessionUseCase) VerifyToken(ctx context.Context, parse func() (int64, string, int64, error)) (*VerifyResult, error) {
	uid, jti, expireAt, err := parse()
	if err != nil {
		// 令牌本身无效（签名错、过期、缺 uid）属于正常业务分支，
		// 不打日志避免被无效令牌刷屏。
		return &VerifyResult{Valid: false}, nil
	}

	if !uc.tokenModeDB {
		return &VerifyResult{Valid: true, UserID: uid, ExpireAt: expireAt, JTI: jti}, nil
	}

	// db 模式必须能定位到具体记录，否则无法判断是否被吊销
	if jti == "" {
		uc.log.Warnw("msg", "db 模式下令牌缺少 jti，拒绝通过", "uid", uid)
		return &VerifyResult{Valid: false}, nil
	}

	tok, err := uc.tokens.FindByJTI(ctx, jti)
	if err != nil {
		// 存储查询失败是真实故障：此时不能放行——db 模式的语义就是
		// 「必须确认未被吊销」，放行等于把严格模式悄悄降级为 self 模式，
		// 被踢掉的设备会因此重新获得访问权。
		uc.log.Errorw("msg", "db 模式下查询令牌失败，拒绝通过",
			"uid", uid, "jti", jti, "err", err)
		return &VerifyResult{Valid: false}, err
	}
	if tok == nil {
		uc.log.Warnw("msg", "db 模式下令牌不存在，拒绝通过",
			"uid", uid, "jti", jti)
		return &VerifyResult{Valid: false}, nil
	}
	if !tok.IsValid(time.Now()) {
		uc.log.Warnw("msg", "db 模式下令牌已失效，拒绝通过",
			"uid", uid, "jti", jti, "revokedBy", tok.RevokedBy)
		return &VerifyResult{Valid: false}, nil
	}

	// 刷新活跃时间：失败不影响校验结论，因此忽略错误（内部已记日志）
	_ = uc.tokens.Touch(ctx, jti, time.Now())

	return &VerifyResult{Valid: true, UserID: uid, ExpireAt: expireAt, JTI: jti}, nil
}

// Logout 主动登出：吊销令牌并移除设备在线标记。
func (uc *SessionUseCase) Logout(ctx context.Context, userID int64, jti, deviceID string) error {
	if jti == "" {
		return ErrInvalidParam
	}

	if err := uc.tokens.Revoke(ctx, jti, RevokeReasonLogout, time.Now()); err != nil {
		uc.log.Errorw("msg", "吊销登出令牌失败",
			"uid", userID, "jti", jti, "err", err)
		return err
	}

	// 清理设备标记：它只影响设备数上限，失败不影响登出语义
	if err := uc.devices.Remove(ctx, userID, DeviceKey(deviceID, jti)); err != nil {
		uc.log.Errorw("msg", "移除登出设备标记失败",
			"uid", userID, "jti", jti, "err", err)
	}

	uc.log.Infow("msg", "用户已登出", "uid", userID, "jti", jti, "deviceId", deviceID)
	return nil
}

// RevokeAll 吊销某用户的全部令牌（供改密使用）。
func (uc *SessionUseCase) RevokeAll(ctx context.Context, userID int64, reason string) error {
	n, err := uc.tokens.RevokeAllByUser(ctx, userID, reason, time.Now())
	if err != nil {
		uc.log.Errorw("msg", "吊销用户全部令牌失败",
			"uid", userID, "reason", reason, "err", err)
		return err
	}

	// 同时清空设备在线标记，让设备数上限从零开始重新计数。
	// 否则改密后有序集合里仍留着旧设备的标记，用户重新登录会被误判为超限。
	if err := uc.devices.Clear(ctx, userID); err != nil {
		uc.log.Errorw("msg", "清空用户设备在线标记失败",
			"uid", userID, "err", err)
	}

	uc.log.Infow("msg", "已吊销用户全部令牌",
		"uid", userID, "reason", reason, "count", n)
	return nil
}

// MaxDevices 返回当前配置的设备数上限（0 表示不限制），供只读查询使用。
func (uc *SessionUseCase) MaxDevices() int { return uc.maxDevices }

// DeviceKey 计算设备在有序集合中的成员标识。
//
// 客户端上报了稳定的 deviceID 时直接用它：这样同一台设备重新登录
// 会覆盖同一个成员，不会自己把自己挤出上限。
// 未上报时回落到 jti：此时每次登录都算一台新设备，是更保守的行为——
// 宁可多踢，也不要让「不报 deviceID 的客户端」无限占用在线位。
func DeviceKey(deviceID, jti string) string {
	if deviceID != "" {
		return "d:" + deviceID
	}
	return "j:" + jti
}

// SortDeviceKeys 让设备标识列表有确定顺序，便于日志与测试断言。
func SortDeviceKeys(keys []string) []string {
	out := append([]string(nil), keys...)
	sort.Strings(out)
	return out
}
