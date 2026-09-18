package server

import "github.com/google/wire"

// ProviderSet 是 server 层的依赖注入集合。
var ProviderSet = wire.NewSet(NewHTTPServer, NewGRPCServer)
