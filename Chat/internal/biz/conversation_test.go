package biz

import "testing"

// TestSingleBizKeySymmetric 守护单聊会话键的核心不变量：
// 同一对用户无论以何种顺序传入，必须得到同一个键。
//
// 为什么值得专门写测试：这个不变量一旦被破坏，后果是「同一对用户出现两个单聊会话」，
// 而且不会报错、不会崩溃，只会在用户侧表现为「聊天记录对不上」——
// 属于最难排查的一类问题。把它钉在测试里，比写在注释里可靠。
func TestSingleBizKeySymmetric(t *testing.T) {
	// 覆盖会暴露字符串比较陷阱的用例：
	// "1000" < "999" 成立（逐字符比 '1' < '9'），若实现先拼接再比较就会出错。
	pairs := [][2]int64{
		{1, 2},
		{999, 1000},
		{9, 10},
		{1, 1_000_000_000},
		{123456789012, 987654321098},
		{7, 9},
	}

	for _, p := range pairs {
		a, b := p[0], p[1]
		if got, want := SingleBizKey(a, b), SingleBizKey(b, a); got != want {
			t.Errorf("SingleBizKey 不对称: SingleBizKey(%d,%d)=%q, SingleBizKey(%d,%d)=%q",
				a, b, got, b, a, want)
		}
	}
}

// TestSingleBizKeyDistinct 确认不同用户对不会产生相同键。
func TestSingleBizKeyDistinct(t *testing.T) {
	seen := map[string][2]int64{}
	pairs := [][2]int64{
		{1, 2}, {2, 3}, {1, 3}, {11, 2}, {1, 12}, {99, 100},
	}

	for _, p := range pairs {
		key := SingleBizKey(p[0], p[1])
		if prev, dup := seen[key]; dup && prev != p {
			t.Fatalf("键冲突: %v 与 %v 都生成了 %q", prev, p, key)
		}
		seen[key] = p
	}
}

// TestSingleBizKeyFormat 锁定键的格式契约。
//
// 表注释与迁移脚本都声明了该格式（'S:' 前缀 + 排序后的两个 ID），
// 若实现悄悄改动，数据库里的历史键会对不上，因此这里显式固化。
func TestSingleBizKeyFormat(t *testing.T) {
	cases := []struct {
		a, b int64
		want string
	}{
		{7, 9, "S:7:9"},
		{9, 7, "S:7:9"},
		{1000, 999, "S:999:1000"},
	}
	for _, c := range cases {
		if got := SingleBizKey(c.a, c.b); got != c.want {
			t.Errorf("SingleBizKey(%d,%d) = %q, 期望 %q", c.a, c.b, got, c.want)
		}
	}
}

// TestGroupBizKeyFormat 锁定群聊键的格式契约。
func TestGroupBizKeyFormat(t *testing.T) {
	if got, want := GroupBizKey(359542605399101440), "G:359542605399101440"; got != want {
		t.Errorf("GroupBizKey = %q, 期望 %q", got, want)
	}
}

// TestBizKeyPrefixesDisjoint 确认单聊键与群聊键不会互相撞形。
//
// 两者共用 (type, biz_key) 唯一索引，前缀确保即使某个用户 ID
// 恰好拼出与群聊 ID 相同的数字串，也不会产生相同的键。
func TestBizKeyPrefixesDisjoint(t *testing.T) {
	single := SingleBizKey(359542605399101440, 1)
	group := GroupBizKey(359542605399101440)
	if single == group {
		t.Fatalf("单聊键与群聊键相同: %q", single)
	}
}
