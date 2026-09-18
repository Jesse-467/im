package server

import "github.com/google/wire"

// ProviderSet 是 server 层的依赖注入集合。
//
// 当前骨架阶段只有 HTTP 传输层；Chat 的 gRPC 服务端将在业务层落地后接入。
var ProviderSet = wire.NewSet(NewHTTPServer)
