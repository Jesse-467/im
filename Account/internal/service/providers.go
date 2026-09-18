package service

import "github.com/google/wire"

// ProviderSet 是 service 层的依赖注入集合。
var ProviderSet = wire.NewSet(NewAccountService)
