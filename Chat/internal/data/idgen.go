package data

import (
	"fmt"
	"os"
	"strconv"
	"time"

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

// NewSnowflake 构造雪花发号器。
//
// 单独暴露为 provider：除了 biz 层需要它发号，WebSocket 网关也需要它
// 生成连接 ID，因此不能只藏在 biz.IDGenerator 接口背后。
//
// 节点号来自配置：容器环境下主机名是随机的，靠主机名哈希推导会在重启后漂移，
// 显式注入（如 StatefulSet 序号）才能保证同一副本长期占用同一节点号。
func NewSnowflake(c *conf.Config) (*xid.Generator, error) {
	gen, err := xid.New(c.App.NodeID)
	if err != nil {
		return nil, fmt.Errorf("data: 初始化 ID 生成器失败: %w", err)
	}
	return gen, nil
}

// NewIDGenerator 基于雪花发号器构造业务层的 ID 生成接口。
func NewIDGenerator(gen *xid.Generator) biz.IDGenerator {
	return &idGenerator{gen: gen}
}

// Next 生成下一个全局唯一且趋势递增的 ID。
func (g *idGenerator) Next() (int64, error) { return g.gen.Next() }

// NodeID 是网关节点标识，用于跨节点路由与在线状态登记。
type NodeID string

// NewNodeID 生成本节点的唯一标识。
//
// 优先取配置中的 NODE_ID（容器编排下应显式注入，如 StatefulSet 序号），
// 未配置时退化为「主机名 + 进程启动时间」：
//   - 只用主机名不够：同机多实例会撞成同一个节点，导致路由表互相覆盖；
//   - 加上启动时间可区分同机多实例，代价是重启后标识变化——
//     这对路由表无害（旧标识会被惰性清理）。
func NewNodeID(c *conf.Config) NodeID {
	if v := os.Getenv("NODE_ID"); v != "" {
		return NodeID(v)
	}

	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	return NodeID(host + "-" + strconv.FormatInt(time.Now().UnixNano()%1_000_000, 36))
}
