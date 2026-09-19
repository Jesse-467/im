package data

import (
	"fmt"

	"github.com/Jesse-467/im/Chat/internal/biz"
	"github.com/Jesse-467/im/Chat/internal/conf"
	"github.com/Jesse-467/im/Chat/internal/xid"
)

// 编译期断言：发号器必须满足业务层声明的接口。
var _ biz.IDGenerator = (*idGenerator)(nil)

// idGenerator 把 xid 的雪花发号器适配为 biz.IDGenerator。
//
// 适配而不是让 biz 直接依赖 xid：发号器属于基础设施能力，若业务层直接持有它，
// 单测就无法替换为可预测序列（例如断言"会话 ID 被用作 biz_key 的一部分"）。
// 这里只做一层薄封装，不额外引入状态，开销可以忽略。
type idGenerator struct {
	gen *xid.Generator
}

// NewIDGenerator 构造 ID 生成器。
//
// 节点号来自配置：容器环境下主机名是随机的，靠主机名哈希推导会在重启后漂移，
// 显式注入（如 StatefulSet 序号）才能保证同一副本长期占用同一节点号。
func NewIDGenerator(c *conf.Config) (biz.IDGenerator, error) {
	gen, err := xid.New(c.App.NodeID)
	if err != nil {
		return nil, fmt.Errorf("data: 初始化 ID 生成器失败: %w", err)
	}
	return &idGenerator{gen: gen}, nil
}

// Next 生成下一个全局唯一且趋势递增的 ID。
func (g *idGenerator) Next() (int64, error) { return g.gen.Next() }
