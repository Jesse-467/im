// Package auth 负责 JWT 的签发与校验，以及与 context 的用户身份传递。
//
// 采用 HS256 对称签名：本服务既签发也校验，无需暴露公钥。
// uid 放在自定义 claim 中，校验通过后写入 context，供 HTTP / gRPC 两个传输层共用。
package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ctxUserIDKey 是 context 中用户 ID 的键。使用私有类型避免与其他包的键冲突。
type ctxUserIDKey struct{}

// Errors
var (
	ErrMissingToken = errors.New("缺少访问令牌")
	ErrInvalidToken = errors.New("访问令牌无效或已过期")
)

// Token 是签发结果。
type Token struct {
	AccessToken string
	// ExpireAt 为过期时间（Unix 秒）
	ExpireAt int64
	// JTI 是令牌的唯一标识（JWT 的 jti claim）。
	//
	// 为什么需要它：令牌本身很长且包含签名，若直接把它当数据库主键，
	// 既浪费存储又把凭证暴露给任何能读该表的人。用随机 ID 作为替身，
	// 数据库只存 jti，校验时从 JWT 里取出 jti 去比对，同样能表达
	// 「这个令牌是否被吊销」，且没有泄露风险。
	JTI string
}

// Sign 为指定用户签发访问令牌。
func Sign(secret string, userID int64, ttl time.Duration) (*Token, error) {
	if secret == "" {
		return nil, errors.New("auth: JWT 密钥为空")
	}
	if ttl <= 0 {
		return nil, errors.New("auth: 令牌有效期必须大于 0")
	}

	now := time.Now()
	expireAt := now.Add(ttl)

	jti, err := newJTI()
	if err != nil {
		return nil, fmt.Errorf("auth: 生成令牌标识失败: %w", err)
	}

	claims := jwt.MapClaims{
		"uid": userID,
		"jti": jti,
		"iat": now.Unix(),
		"exp": expireAt.Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		return nil, fmt.Errorf("auth: 签发令牌失败: %w", err)
	}

	return &Token{AccessToken: signed, ExpireAt: expireAt.Unix(), JTI: jti}, nil
}

// Parse 校验访问令牌并返回用户 ID。
//
// 显式锁定签名算法为 HS256，防止算法混淆攻击（如篡改为 none 或 RS256）。
func Parse(secret, tokenString string) (userID int64, expireAt int64, err error) {
	uid, _, expireAt, err := ParseWithJTI(secret, tokenString)
	return uid, expireAt, err
}

// ParseWithJTI 校验访问令牌并同时返回 jti。
//
// 与 Parse 分开而不是直接改签名：Chat 服务也依赖 Parse（它编不出
// Account 的令牌、也不需要 jti），保持原签名可以让两侧互不影响。
func ParseWithJTI(secret, tokenString string) (userID int64, jti string, expireAt int64, err error) {
	if tokenString == "" {
		return 0, "", 0, ErrMissingToken
	}

	claims := jwt.MapClaims{}
	_, err = jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("auth: 非预期的签名算法 %v", t.Header["alg"])
		}
		return []byte(secret), nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil {
		return 0, "", 0, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}

	switch v := claims["uid"].(type) {
	case float64:
		userID = int64(v)
	case int64:
		userID = v
	case int:
		userID = int64(v)
	default:
		return 0, "", 0, fmt.Errorf("%w: uid claim 缺失或类型不正确", ErrInvalidToken)
	}
	if userID <= 0 {
		return 0, "", 0, fmt.Errorf("%w: uid 非法", ErrInvalidToken)
	}

	// jti 缺失不视为令牌无效：它只影响「能否被吊销」，
	// 而 TokenMode=self 下本就不需要它。由调用方按模式决定是否要求非空。
	jti, _ = claims["jti"].(string)

	if exp, ok := claims["exp"].(float64); ok {
		expireAt = int64(exp)
	}
	return userID, jti, expireAt, nil
}

// newJTI 生成一个随机且唯一的令牌标识。
//
// 用 128 位随机数而非自增序列：jti 会出现在令牌里，可预测的序列
// 会让攻击者有机会猜测其他令牌的标识。随机值不携带任何规律。
func newJTI() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	// 输出为 32 位十六进制：无需第三方 UUID 依赖，长度也在列宽内
	return hex.EncodeToString(b[:]), nil
}

// WithUserID 把用户 ID 写入 context。
func WithUserID(ctx context.Context, userID int64) context.Context {
	return context.WithValue(ctx, ctxUserIDKey{}, userID)
}

// UserIDFromContext 从 context 取出用户 ID。
func UserIDFromContext(ctx context.Context) (int64, bool) {
	uid, ok := ctx.Value(ctxUserIDKey{}).(int64)
	return uid, ok && uid > 0
}
