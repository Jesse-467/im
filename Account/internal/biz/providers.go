package biz

import "github.com/google/wire"

// ProviderSet 是 biz 层的依赖注入集合。
var ProviderSet = wire.NewSet(
	NewUserUseCase,
	NewSessionUseCase,
	ProvideSessionOptions,
)
