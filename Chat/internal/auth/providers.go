package auth

import (
	"context"

	"github.com/google/wire"

	"github.com/Jesse-467/im/Chat/internal/conf"
)

// ProviderSet 是鉴权层的依赖注入集合。
var ProviderSet = wire.NewSet(
	NewVerifier,
	ProvideTokenVerifier,
	ProvideJWTSecret,
)

// JWTSecret 是签名密钥的具名类型。
//
// 用具名类型而非裸 string：Wire 按类型匹配依赖，服务中需要注入的字符串
// 不止一个（密钥、主题名、节点 ID），裸 string 会让它们互相冲突。
type JWTSecret string

// ProvideJWTSecret 从配置提供签名密钥。
//
// 必须导出：Wire 生成的装配代码位于 cmd 包的 main 包中。
func ProvideJWTSecret(c *conf.Config) JWTSecret {
	return JWTSecret(c.App.JWTSecret)
}

// Client 是账号中心客户端中「校验令牌」这一能力的窄接口。
//
// 用窄接口而不是直接依赖 data 层的具体类型：auth 包只需要这一个方法，
// 让 Wire 通过它完成绑定，可以避免 auth 反向依赖 data（那会形成
// auth → data → biz → auth 的环）。
type Client interface {
	VerifyToken(ctx context.Context, accessToken string) (bool, int64, error)
}

// ProvideTokenVerifier 按配置决定是否启用远程令牌校验。
//
// 返回值为 nil 表示关闭远程校验（账号中心自身跑在 self 模式时的合理配置），
// 此时 Verifier 只做本地验签，行为与改造前一致。
//
// 必须导出：Wire 生成的装配代码位于 cmd 包的 main 包中。
func ProvideTokenVerifier(c *conf.Config, client Client) TokenVerifier {
	if !c.App.VerifyTokenRemote {
		return nil
	}
	return client
}
