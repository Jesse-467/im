package biz

import (
	"github.com/Jesse-467/im/Account/internal/conf"
)

// ProvideSessionOptions 从配置推导会话用例的选项。
//
// 放在本包而不是 data 层：这些是业务规则（上限多少、是否严格校验），
// 不是存储实现细节，由业务层自己从配置翻译更合理。
//
// 必须导出：Wire 生成的装配代码位于 cmd 包的 main 包中，
// 无法调用其他包的未导出函数。
func ProvideSessionOptions(c *conf.Config) SessionOptions {
	return SessionOptions{
		MaxDevices:  c.App.MaxDevicesPerUser,
		TokenModeDB: c.App.TokenMode == conf.TokenModeDB,
		DeviceTTL:   c.App.OnlineDeviceTTL,
	}
}
