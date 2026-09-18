// Package auth 负责 JWT 的签发与校验，以及与 context 的用户身份传递。
//
// 采用 HS256 对称签名：本服务既签发也校验，无需暴露公钥。
// uid 放在自定义 claim 中，校验通过后写入 context，供 HTTP / gRPC 两个传输层共用。
package auth

import (
	"context"
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

	claims := jwt.MapClaims{
		"uid": userID,
		"iat": now.Unix(),
		"exp": expireAt.Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		return nil, fmt.Errorf("auth: 签发令牌失败: %w", err)
	}

	return &Token{AccessToken: signed, ExpireAt: expireAt.Unix()}, nil
}

// Parse 校验访问令牌并返回用户 ID。
//
// 显式锁定签名算法为 HS256，防止算法混淆攻击（如篡改为 none 或 RS256）。
func Parse(secret, tokenString string) (userID int64, expireAt int64, err error) {
	if tokenString == "" {
		return 0, 0, ErrMissingToken
	}

	claims := jwt.MapClaims{}
	_, err = jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("auth: 非预期的签名算法 %v", t.Header["alg"])
		}
		return []byte(secret), nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil {
		return 0, 0, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}

	switch v := claims["uid"].(type) {
	case float64:
		userID = int64(v)
	case int64:
		userID = v
	case int:
		userID = int64(v)
	default:
		return 0, 0, fmt.Errorf("%w: uid claim 缺失或类型不正确", ErrInvalidToken)
	}
	if userID <= 0 {
		return 0, 0, fmt.Errorf("%w: uid 非法", ErrInvalidToken)
	}

	if exp, ok := claims["exp"].(float64); ok {
		expireAt = int64(exp)
	}
	return userID, expireAt, nil
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
