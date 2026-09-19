package biz

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	klog "github.com/go-kratos/kratos/v2/log"
)

// ── 测试替身 ────────────────────────────────────────────────────────────────
//
// 用内存实现而不是 mock 框架：这两个接口的方法少、行为简单，
// 手写实现比引入 mock 生成器更直观，也让断言可以直接读。

// memTokenRepo 是 TokenRepo 的内存实现。
type memTokenRepo struct {
	// byJTI 以 jti 为键，模拟 unicité 约束
	byJTI map[string]*AuthToken
	// failSave 为 true 时 Save 返回错误，用于验证故障降级路径
	failSave bool
	// failFind 为 true 时 FindByJTI 返回错误
	failFind bool
}

func newMemTokenRepo() *memTokenRepo {
	return &memTokenRepo{byJTI: map[string]*AuthToken{}}
}

func (r *memTokenRepo) Save(_ context.Context, token *AuthToken) error {
	if r.failSave {
		return errors.New("模拟落库失败")
	}
	cp := *token
	r.byJTI[token.JTI] = &cp
	return nil
}

// CleanupDevice 与真实实现一致：删除同设备的历史行（保留本次 jti）。
func (r *memTokenRepo) CleanupDevice(_ context.Context, userID int64, deviceID, jti string) error {
	if deviceID == "" {
		return nil
	}
	for k, t := range r.byJTI {
		if t.UserID == userID && t.DeviceID == deviceID && k != jti {
			delete(r.byJTI, k)
		}
	}
	return nil
}

func (r *memTokenRepo) FindByJTI(_ context.Context, jti string) (*AuthToken, error) {
	if r.failFind {
		return nil, errors.New("模拟查询失败")
	}
	t, ok := r.byJTI[jti]
	if !ok {
		return nil, nil
	}
	cp := *t
	return &cp, nil
}

func (r *memTokenRepo) FindByDevice(_ context.Context, userID int64, deviceKey string) (*AuthToken, error) {
	for _, t := range r.byJTI {
		if t.UserID != userID {
			continue
		}
		if DeviceKey(t.DeviceID, t.JTI) == deviceKey {
			cp := *t
			return &cp, nil
		}
	}
	return nil, nil
}

func (r *memTokenRepo) Revoke(_ context.Context, jti, reason string, at time.Time) error {
	t, ok := r.byJTI[jti]
	if !ok {
		return nil
	}
	if t.RevokedAt == nil {
		// 与真实实现一致：只记录首次吊销
		t.RevokedAt = &at
		t.RevokedBy = reason
	}
	return nil
}

func (r *memTokenRepo) RevokeAllByUser(_ context.Context, userID int64, reason string, at time.Time) (int, error) {
	n := 0
	for _, t := range r.byJTI {
		if t.UserID == userID && t.RevokedAt == nil {
			cp := at
			t.RevokedAt = &cp
			t.RevokedBy = reason
			n++
		}
	}
	return n, nil
}

func (r *memTokenRepo) Touch(_ context.Context, jti string, at time.Time) error {
	if t, ok := r.byJTI[jti]; ok {
		t.LastSeenAt = at
	}
	return nil
}

// memDeviceStore 是 DeviceStore 的内存实现，语义对齐 Redis 有序集合。
type memDeviceStore struct {
	// scores 为 userID -> member -> score（Unix 毫秒）
	scores map[int64]map[string]float64
	failAdd bool
}

func newMemDeviceStore() *memDeviceStore {
	return &memDeviceStore{scores: map[int64]map[string]float64{}}
}

func (s *memDeviceStore) Add(_ context.Context, userID int64, member string, at time.Time) error {
	if s.failAdd {
		return errors.New("模拟登记失败")
	}
	if s.scores[userID] == nil {
		s.scores[userID] = map[string]float64{}
	}
	// 与 ZADD 一致：同一 member 重复添加只更新 score
	s.scores[userID][member] = float64(at.UnixMilli())
	return nil
}

func (s *memDeviceStore) Oldest(_ context.Context, userID int64, n int) ([]string, error) {
	if n <= 0 {
		return nil, nil
	}
	all := s.sortedMembers(userID)
	if n > len(all) {
		n = len(all)
	}
	return all[:n], nil
}

func (s *memDeviceStore) Keys(_ context.Context, userID int64) ([]string, error) {
	return s.sortedMembers(userID), nil
}

func (s *memDeviceStore) Remove(_ context.Context, userID int64, member string) error {
	delete(s.scores[userID], member)
	return nil
}

func (s *memDeviceStore) Clear(_ context.Context, userID int64) error {
	delete(s.scores, userID)
	return nil
}

// sortedMembers 按 score 升序（再按 member 字典序打破并列）返回成员。
//
// 并列时按成员名排序，是为了让测试结果稳定：真实 Redis 在 score 相同时
// 按 member 字典序排列，这里刻意对齐该行为。
func (s *memDeviceStore) sortedMembers(userID int64) []string {
	m := s.scores[userID]
	out := make([]string, 0, len(m))
	for member := range m {
		out = append(out, member)
	}
	sort.Slice(out, func(i, j int) bool {
		if m[out[i]] != m[out[j]] {
			return m[out[i]] < m[out[j]]
		}
		return out[i] < out[j]
	})
	return out
}

// ── 构造辅助 ────────────────────────────────────────────────────────────────

func newTestSession(maxDevices int, dbMode bool) (*SessionUseCase, *memTokenRepo, *memDeviceStore) {
	repo := newMemTokenRepo()
	store := newMemDeviceStore()
	uc := NewSessionUseCase(repo, store, klog.DefaultLogger, SessionOptions{
		MaxDevices:  maxDevices,
		TokenModeDB: dbMode,
	})
	return uc, repo, store
}

// issue 模拟一次「签发 + 登记设备」，返回 jti。
func issue(t *testing.T, uc *SessionUseCase, userID int64, deviceID string, at time.Time) string {
	t.Helper()
	jti := "jti-" + deviceID
	_, err := uc.RegisterDevice(context.Background(), &AuthToken{
		UserID:    userID,
		JTI:       jti,
		DeviceID:  deviceID,
		Platform:  "web",
		ExpireAt:  at.Add(time.Hour),
		CreatedAt: at,
	})
	if err != nil {
		t.Fatalf("登记设备 %s 失败: %v", deviceID, err)
	}
	return jti
}

// ── 多设备在线上限 ──────────────────────────────────────────────────────────

// TestRegisterDeviceEvictsOldest 验证超出上限时踢出最早登录的设备。
//
// 这是本功能的核心不变量：淘汰顺序必须是「按上线时间从早到晚」，
// 否则会把用户刚登录的设备踢掉，体验上等同于随机掉线。
func TestRegisterDeviceEvictsOldest(t *testing.T) {
	uc, repo, _ := newTestSession(2, true)
	base := time.Now()

	issue(t, uc, 1001, "device-a", base)
	issue(t, uc, 1001, "device-b", base.Add(time.Second))

	// 第三台设备登录，应把最早的 device-a 踢掉
	evicted, err := uc.RegisterDevice(context.Background(), &AuthToken{
		UserID:    1001,
		JTI:       "jti-device-c",
		DeviceID:  "device-c",
		ExpireAt:  base.Add(time.Hour),
		CreatedAt: base.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatalf("第三次登录不应报错: %v", err)
	}

	want := []string{"d:device-a"}
	if len(evicted) != 1 || evicted[0] != want[0] {
		t.Fatalf("应踢出 %v，实际 %v", want, evicted)
	}

	// 被踢设备的令牌必须被吊销，否则它仍能继续使用
	tok, err := repo.FindByJTI(context.Background(), "jti-device-a")
	if err != nil {
		t.Fatalf("查询被踢令牌失败: %v", err)
	}
	if tok == nil || tok.RevokedAt == nil {
		t.Fatal("被踢设备的令牌应当已被吊销")
	}
	if tok.RevokedBy != RevokeReasonEvicted {
		t.Fatalf("吊销原因应为 %s，实际 %s", RevokeReasonEvicted, tok.RevokedBy)
	}

	// 后登录的两台设备必须仍然有效
	for _, jti := range []string{"jti-device-b", "jti-device-c"} {
		tok, _ := repo.FindByJTI(context.Background(), jti)
		if tok == nil || tok.RevokedAt != nil {
			t.Fatalf("令牌 %s 不应被吊销", jti)
		}
	}
}

// TestRegisterDeviceNoLimitWhenZero 验证上限为 0 时不限制设备数。
//
// 0 表达「不限制」而不是「不允许任何设备」：后者会让所有登录立刻失败，
// 是把配置语义搞反的典型后果。
func TestRegisterDeviceNoLimitWhenZero(t *testing.T) {
	uc, _, store := newTestSession(0, true)
	base := time.Now()

	for i, dev := range []string{"a", "b", "c", "d", "e"} {
		evicted := func() []string {
			res, err := uc.RegisterDevice(context.Background(), &AuthToken{
				UserID:    2001,
				JTI:       "jti-" + dev,
				DeviceID:  dev,
				ExpireAt:  base.Add(time.Hour),
				CreatedAt: base.Add(time.Duration(i) * time.Second),
			})
			if err != nil {
				t.Fatalf("登记设备 %s 失败: %v", dev, err)
			}
			return res
		}()
		if len(evicted) != 0 {
			t.Fatalf("上限为 0 时不应踢出任何设备，实际踢出 %v", evicted)
		}
	}

	keys, _ := store.Keys(context.Background(), 2001)
	if len(keys) != 5 {
		t.Fatalf("应记录 5 台设备，实际 %d 台", len(keys))
	}
}

// TestRegisterDeviceSameDeviceReplaces 验证同一设备重新登录不会占用新的在线位。
//
// 若不做「按设备复用」，同一台设备反复登录会把自己的位置占满，
// 表现为「只是重新登录一次，其他端就全被踢了」。
func TestRegisterDeviceSameDeviceReplaces(t *testing.T) {
	uc, _, store := newTestSession(2, true)
	base := time.Now()

	issue(t, uc, 3001, "device-a", base)
	issue(t, uc, 3001, "device-b", base.Add(time.Second))
	// 设备 a 重新登录（换了一个新令牌，但设备标识不变）
	issue(t, uc, 3001, "device-a", base.Add(2*time.Second))

	keys, _ := store.Keys(context.Background(), 3001)
	if len(keys) != 2 {
		t.Fatalf("同一设备重新登录后应仍是 2 台，实际 %d 台: %v", len(keys), keys)
	}
}

// TestRegisterDeviceSaveFailure 验证落库失败时不登记设备。
//
// 顺序不变量：令牌表是「令牌是否有效」的权威，有序集合只是计数辅助。
// 若先登记设备而落库失败，会出现「显示在线但令牌根本不存在」的假在线。
func TestRegisterDeviceSaveFailure(t *testing.T) {
	uc, repo, store := newTestSession(2, true)
	repo.failSave = true

	_, err := uc.RegisterDevice(context.Background(), &AuthToken{
		UserID:    4001,
		JTI:       "jti-x",
		DeviceID:  "device-x",
		ExpireAt:  time.Now().Add(time.Hour),
		CreatedAt: time.Now(),
	})
	if err == nil {
		t.Fatal("落库失败时应返回错误")
	}

	keys, _ := store.Keys(context.Background(), 4001)
	if len(keys) != 0 {
		t.Fatalf("落库失败时不应登记设备，实际 %v", keys)
	}
}

// TestRegisterDeviceAddFailureDegrades 验证设备登记失败时登录仍可用。
//
// 这是刻意的可用性取舍：令牌已经可用，不应因为 Redis 抖动让用户登录不了。
// 代价是多设备上限本次不生效，属于可接受的降级。
func TestRegisterDeviceAddFailureDegrades(t *testing.T) {
	uc, repo, store := newTestSession(2, true)
	store.failAdd = true

	evicted, err := uc.RegisterDevice(context.Background(), &AuthToken{
		UserID:    5001,
		JTI:       "jti-y",
		DeviceID:  "device-y",
		ExpireAt:  time.Now().Add(time.Hour),
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("设备登记失败不应让登录失败: %v", err)
	}
	if len(evicted) != 0 {
		t.Fatalf("登记失败时不应报告踢出设备，实际 %v", evicted)
	}

	// 令牌仍应落库成功，保证登录结果可用
	tok, _ := repo.FindByJTI(context.Background(), "jti-y")
	if tok == nil {
		t.Fatal("设备登记失败时令牌仍应已落库")
	}
}

// TestMaxDevicesReportsConfiguredValue 验证只读查询返回配置值。
func TestMaxDevicesReportsConfiguredValue(t *testing.T) {
	uc, _, _ := newTestSession(5, true)
	if got := uc.MaxDevices(); got != 5 {
		t.Fatalf("MaxDevices() = %d, 期望 5", got)
	}
}

// ── 双模式令牌校验 ──────────────────────────────────────────────────────────

// parseOK 返回一个总是成功的解析函数。
func parseOK(uid int64, jti string, exp int64) func() (int64, string, int64, error) {
	return func() (int64, string, int64, error) { return uid, jti, exp, nil }
}

// TestVerifySelfModeSkipsStorage 验证 self 模式不查存储。
//
// 这是 self 模式的核心价值：即使数据库/Redis 完全不可用，
// 令牌校验仍能正常完成（零存储依赖）。
func TestVerifySelfModeSkipsStorage(t *testing.T) {
	uc, repo, _ := newTestSession(0, false)
	repo.failFind = true // 一旦回查就会失败，从而暴露「本不该查」

	res, err := uc.VerifyToken(context.Background(), parseOK(6001, "any-jti", time.Now().Add(time.Hour).Unix()))
	if err != nil {
		t.Fatalf("self 模式不应因存储不可用而失败: %v", err)
	}
	if !res.Valid {
		t.Fatal("self 模式下令牌应当有效")
	}
	if res.UserID != 6001 {
		t.Fatalf("uid 应为 6001，实际 %d", res.UserID)
	}
}

// TestVerifySelfModeAcceptsRevokedToken 验证 self 模式无法感知吊销。
//
// 这不是缺陷而是模式的固有取舍，必须用测试固定下来：
// 若有人误以为 self 模式也能即时踢下线，会做出错误的安全假设。
func TestVerifySelfModeAcceptsRevokedToken(t *testing.T) {
	uc, repo, _ := newTestSession(0, false)
	jti := "jti-revoked"
	_ = repo.Save(context.Background(), &AuthToken{
		UserID: 6002, JTI: jti, DeviceID: "d1", ExpireAt: time.Now().Add(time.Hour),
	})
	_ = repo.Revoke(context.Background(), jti, RevokeReasonEvicted, time.Now())

	res, _ := uc.VerifyToken(context.Background(), parseOK(6002, jti, time.Now().Add(time.Hour).Unix()))
	if !res.Valid {
		t.Fatal("self 模式按设计仍会通过已吊销的令牌")
	}
}

// TestVerifyDBModeRejectsRevokedToken 验证 db 模式拒绝已吊销的令牌。
//
// 这是多设备踢下线能「即时生效」的关键：被踢设备的令牌在自然过期前
// 就会被校验拦下。
//
// 注意断言的是 (valid=false, err=nil)：吊销属于**正常业务分支**，
// 若返回 error，调用方按 err != nil 处理会对外报 500，
// 把一次正常的重新登录变成服务故障。
func TestVerifyDBModeRejectsRevokedToken(t *testing.T) {
	uc, repo, _ := newTestSession(0, true)
	jti := "jti-evicted"
	_ = repo.Save(context.Background(), &AuthToken{
		UserID: 7001, JTI: jti, DeviceID: "d1", ExpireAt: time.Now().Add(time.Hour),
	})
	_ = repo.Revoke(context.Background(), jti, RevokeReasonEvicted, time.Now())

	res, err := uc.VerifyToken(context.Background(), parseOK(7001, jti, time.Now().Add(time.Hour).Unix()))
	if err != nil {
		t.Fatalf("吊销属于业务分支，必须返回 nil 错误（否则调用方会报 500）: %v", err)
	}
	if res.Valid {
		t.Fatal("db 模式下已吊销的令牌必须被拒绝")
	}
}

// TestVerifyDBModeAcceptsValidToken 验证 db 模式放行有效令牌。
func TestVerifyDBModeAcceptsValidToken(t *testing.T) {
	uc, repo, _ := newTestSession(0, true)
	jti := "jti-good"
	_ = repo.Save(context.Background(), &AuthToken{
		UserID: 7002, JTI: jti, DeviceID: "d1", ExpireAt: time.Now().Add(time.Hour),
	})

	res, err := uc.VerifyToken(context.Background(), parseOK(7002, jti, time.Now().Add(time.Hour).Unix()))
	if err != nil {
		t.Fatalf("有效令牌不应报错: %v", err)
	}
	if !res.Valid {
		t.Fatal("db 模式下有效令牌应当通过")
	}
}

// TestVerifyDBModeRejectsMissingToken 验证 db 模式拒绝存储中不存在的令牌。
//
// 场景：令牌是用旧密钥签发的（密钥轮换后仍能自校验通过），
// 或存储被清理过。此时必须拒绝，否则旧令牌会成为后门。
func TestVerifyDBModeRejectsMissingToken(t *testing.T) {
	uc, _, _ := newTestSession(0, true)

	res, err := uc.VerifyToken(context.Background(), parseOK(7003, "jti-unknown", time.Now().Add(time.Hour).Unix()))
	if err != nil {
		t.Fatalf("不存在属于业务分支，必须返回 nil 错误: %v", err)
	}
	if res.Valid {
		t.Fatal("db 模式下存储中不存在的令牌必须被拒绝")
	}
}

// TestVerifyDBModeRejectsEmptyJTI 验证 db 模式拒绝缺少 jti 的令牌。
//
// 没有 jti 就无法回查，若放行等于把严格模式悄悄降级为 self 模式。
func TestVerifyDBModeRejectsEmptyJTI(t *testing.T) {
	uc, _, _ := newTestSession(0, true)

	res, err := uc.VerifyToken(context.Background(), parseOK(7004, "", time.Now().Add(time.Hour).Unix()))
	if err != nil {
		t.Fatalf("缺少 jti 属于业务分支，必须返回 nil 错误: %v", err)
	}
	if res.Valid {
		t.Fatal("缺少 jti 的令牌必须被拒绝")
	}
}

// TestVerifyDBModeFailsClosedOnStorageError 验证存储故障时拒绝放行。
//
// fail-closed 是安全底线：db 模式的语义是「必须确认未被吊销」，
// 存储查不通时放行，等于让被踢掉的设备重新获得访问权。
func TestVerifyDBModeFailsClosedOnStorageError(t *testing.T) {
	uc, repo, _ := newTestSession(0, true)
	repo.failFind = true

	res, err := uc.VerifyToken(context.Background(), parseOK(7005, "jti-z", time.Now().Add(time.Hour).Unix()))
	if err == nil {
		t.Fatal("存储故障时应返回错误而不是静默放行")
	}
	if res.Valid {
		t.Fatal("存储故障时必须拒绝放行")
	}
}

// TestVerifyInvalidTokenStaysSilent 验证令牌本身无效时不报错。
//
// 无效令牌是客户端可预期的正常分支（过期、伪造），
// 返回 valid=false 即可，不应产生错误日志被刷屏。
func TestVerifyInvalidTokenStaysSilent(t *testing.T) {
	uc, _, _ := newTestSession(0, true)

	res, err := uc.VerifyToken(context.Background(), func() (int64, string, int64, error) {
		return 0, "", 0, errors.New("签名不正确")
	})
	if err != nil {
		t.Fatalf("无效令牌不应报错: %v", err)
	}
	if res.Valid {
		t.Fatal("无效令牌必须返回 valid=false")
	}
}

// ── 登出与全量吊销 ──────────────────────────────────────────────────────────

// TestLogoutRevokesAndFreesSlot 验证登出既吊销令牌又释放设备位。
//
// 只吊销不释放的话，登出过的设备会一直占着上限名额，
// 用户会发现自己「没登录任何地方却提示设备数超限」。
func TestLogoutRevokesAndFreesSlot(t *testing.T) {
	uc, repo, store := newTestSession(2, true)
	base := time.Now()

	jti := issue(t, uc, 8001, "device-a", base)

	if err := uc.Logout(context.Background(), 8001, jti, "device-a"); err != nil {
		t.Fatalf("登出失败: %v", err)
	}

	tok, _ := repo.FindByJTI(context.Background(), jti)
	if tok == nil || tok.RevokedAt == nil {
		t.Fatal("登出后令牌应当已被吊销")
	}
	if tok.RevokedBy != RevokeReasonLogout {
		t.Fatalf("吊销原因应为 %s，实际 %s", RevokeReasonLogout, tok.RevokedBy)
	}

	keys, _ := store.Keys(context.Background(), 8001)
	if len(keys) != 0 {
		t.Fatalf("登出后设备在线位应被释放，实际仍有 %v", keys)
	}
}

// TestRevokeAllClearsDeviceSlots 验证改密后设备位一并清空。
//
// 不清空的话，用户改密后重新登录会被旧设备的残留标记判为超限，
// 表现为「刚改完密码，所有端都登不进去」。
func TestRevokeAllClearsDeviceSlots(t *testing.T) {
	uc, repo, store := newTestSession(5, true)
	base := time.Now()

	issue(t, uc, 9001, "device-a", base)
	issue(t, uc, 9001, "device-b", base.Add(time.Second))

	if err := uc.RevokeAll(context.Background(), 9001, RevokeReasonPasswordReset); err != nil {
		t.Fatalf("全量吊销失败: %v", err)
	}

	for _, jti := range []string{"jti-device-a", "jti-device-b"} {
		tok, _ := repo.FindByJTI(context.Background(), jti)
		if tok == nil || tok.RevokedAt == nil {
			t.Fatalf("令牌 %s 应当已被吊销", jti)
		}
	}

	keys, _ := store.Keys(context.Background(), 9001)
	if len(keys) != 0 {
		t.Fatalf("设备在线位应被清空，实际仍有 %v", keys)
	}
}

// TestLogoutRejectsEmptyJTI 验证缺少 jti 时拒绝登出。
func TestLogoutRejectsEmptyJTI(t *testing.T) {
	uc, _, _ := newTestSession(0, true)
	if err := uc.Logout(context.Background(), 1, "", ""); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("应返回 ErrInvalidParam，实际 %v", err)
	}
}

// ── 设备标识 ────────────────────────────────────────────────────────────────

// TestDeviceKey 验证设备标识的生成规则。
//
// 有 deviceID 时优先用它：这让同一设备重新登录落在同一个成员上。
// 没有时回落到 jti：此时每次登录都算一台新设备，是更保守的行为。
func TestDeviceKey(t *testing.T) {
	cases := []struct {
		name     string
		deviceID string
		jti      string
		want     string
	}{
		{"有设备标识时优先使用", "abc", "j1", "d:abc"},
		{"无设备标识时回落到 jti", "", "j1", "j:j1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DeviceKey(tc.deviceID, tc.jti); got != tc.want {
				t.Fatalf("DeviceKey(%q,%q) = %q, 期望 %q", tc.deviceID, tc.jti, got, tc.want)
			}
		})
	}
}

// TestAuthTokenIsValid 验证令牌有效性判定的两个条件。
//
// 必须同时看「未吊销」与「未过期」：只看一个都会放过本该失效的令牌。
func TestAuthTokenIsValid(t *testing.T) {
	now := time.Now()
	future := now.Add(time.Hour)
	past := now.Add(-time.Hour)
	revoked := now.Add(-time.Minute)

	cases := []struct {
		name string
		tok  AuthToken
		want bool
	}{
		{"未吊销且未过期", AuthToken{ExpireAt: future}, true},
		{"已过期", AuthToken{ExpireAt: past}, false},
		{"已吊销", AuthToken{ExpireAt: future, RevokedAt: &revoked}, false},
		{"既吊销又过期", AuthToken{ExpireAt: past, RevokedAt: &revoked}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.tok.IsValid(now); got != tc.want {
				t.Fatalf("IsValid() = %v, 期望 %v", got, tc.want)
			}
		})
	}
}
