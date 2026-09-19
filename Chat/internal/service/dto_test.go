package service

import (
	"encoding/json"
	"testing"
)

// 雪花 ID 会超出 JavaScript 的 Number.MAX_SAFE_INTEGER（9007199254740991）。
// 下面统一用这个真实量级的样本，确保测试覆盖到"会丢精度"的区间。
const snowflakeSample = int64(359572627845451776)

// TestIDMarshalsAsString 验证对外输出的 ID 是 JSON 字符串。
//
// 这是接口契约的一部分：若序列化成数字，浏览器用 JSON.parse 解析时
// 会把 359572627845451776 变成 359572627845451800，且没有任何异常可捕获。
// 客户端再把这个被污染的值回传，就会指向一个不存在的会话。
func TestIDMarshalsAsString(t *testing.T) {
	type payload struct {
		ID ID `json:"id"`
	}

	b, err := json.Marshal(payload{ID: ID(snowflakeSample)})
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}

	want := `{"id":"359572627845451776"}`
	if string(b) != want {
		t.Fatalf("序列化结果 = %s, 期望 %s", b, want)
	}
}

// TestIDRoundTripKeepsPrecision 验证序列化再解析后数值不变。
//
// 若中途退化成数字，这个往返就会丢精度——正是线上问题的复现路径。
func TestIDRoundTripKeepsPrecision(t *testing.T) {
	type payload struct {
		ID ID `json:"id"`
	}

	b, err := json.Marshal(payload{ID: ID(snowflakeSample)})
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}

	var got payload
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}
	if int64(got.ID) != snowflakeSample {
		t.Fatalf("往返后 ID = %d, 期望 %d", got.ID, snowflakeSample)
	}
}

// TestIDAcceptsBothLiterals 验证 ID 既能被字符串也能被数字反序列化。
//
// 输入侧必须两种都收：老客户端仍在传数字，新客户端传字符串，
// 若只认一种，另一类客户端会在升级期直接失败。
func TestIDAcceptsBothLiterals(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"字符串形式", `{"id":"359572627845451776"}`},
		{"数字形式", `{"id":359572627845451776}`},
		{"null", `{"id":null}`},
		{"缺省", `{}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got struct {
				ID ID `json:"id"`
			}
			if err := json.Unmarshal([]byte(tc.raw), &got); err != nil {
				t.Fatalf("解析 %s 失败: %v", tc.raw, err)
			}
			want := snowflakeSample
			if tc.name == "null" || tc.name == "缺省" {
				want = 0
			}
			if int64(got.ID) != want {
				t.Fatalf("ID = %d, 期望 %d", got.ID, want)
			}
		})
	}
}

// TestIDRejectsGarbage 验证非法输入被拒绝而不是静默变成 0。
//
// 静默变成 0 会让请求继续往下走，最终报出「会话不存在」这种
// 指向错误的错误信息，掩盖了真正的参数格式问题。
func TestIDRejectsGarbage(t *testing.T) {
	for _, raw := range []string{`{"id":"abc"}`, `{"id":"1.5"}`, `{"id":{}}`, `{"id":[]}`} {
		var got struct {
			ID ID `json:"id"`
		}
		if err := json.Unmarshal([]byte(raw), &got); err == nil {
			t.Errorf("非法输入 %s 应当被拒绝，得到 %d", raw, got.ID)
		}
	}
}

// TestConversationRefAcceptsStringID 验证会话引用接受字符串形式的会话 ID。
//
// 这是客户端最常走的路径：会话列表返回字符串 ID，客户端原样回传。
func TestConversationRefAcceptsStringID(t *testing.T) {
	var ref conversationRef
	raw := `{"conversationId":"359572627845451776","groupId":"359572627845451776"}`
	if err := json.Unmarshal([]byte(raw), &ref); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if int64(ref.ConversationID) != snowflakeSample {
		t.Fatalf("ConversationID = %d, 期望 %d", ref.ConversationID, snowflakeSample)
	}
}

// TestChatMsgDTOOutputsStringIDs 验证消息结构里的 ID 字段全部输出为字符串。
//
// 逐个字段断言而不是整体比对：一旦有人新增了 ID 字段却忘了转换类型，
// 这里能直接指出是哪个字段漏了。
func TestChatMsgDTOOutputsStringIDs(t *testing.T) {
	dto := chatMsgDTO{
		ID:             ID(snowflakeSample),
		ConversationID: ID(snowflakeSample + 1),
		SenderID:       ID(snowflakeSample + 2),
		Seq:            7,
		Content:        "hi",
	}

	b, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("解析自身输出失败: %v", err)
	}

	for _, field := range []string{"id", "conversationId", "senderId"} {
		v, ok := raw[field]
		if !ok {
			t.Fatalf("输出缺少字段 %s", field)
		}
		if len(v) == 0 || v[0] != '"' {
			t.Errorf("字段 %s 必须输出为字符串，实际 %s", field, v)
		}
	}

	// Seq 不是雪花值，保持数字类型便于客户端直接做算术比较
	if v := raw["seq"]; len(v) == 0 || v[0] == '"' {
		t.Errorf("字段 seq 应保持数字类型，实际 %s", v)
	}
}

// TestMemberIDListAcceptsMixedLiterals 验证成员 ID 列表可混用字符串与数字。
//
// 真实客户端在灰度期会出现这种混合：老版本传数字、新版本传字符串。
func TestMemberIDListAcceptsMixedLiterals(t *testing.T) {
	var req struct {
		MemberIDs memberIDList `json:"memberIds"`
	}
	raw := `{"memberIds":["359572627845451776",12345,"67890"]}`
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("解析失败: %v", err)
	}

	got := req.MemberIDs.int64s()
	want := []int64{359572627845451776, 12345, 67890}
	if len(got) != len(want) {
		t.Fatalf("成员数 = %d, 期望 %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 个成员 = %d, 期望 %d", i, got[i], want[i])
		}
	}
}

// TestMemberIDListSkipsNonPositive 验证非正数成员被跳过。
//
// 0 与负数不是合法用户 ID，留着它们会让下游的批量查询与权限校验
// 产生无意义的调用；直接过滤比让它们穿到业务层更干净。
func TestMemberIDListSkipsNonPositive(t *testing.T) {
	var req struct {
		MemberIDs memberIDList `json:"memberIds"`
	}
	if err := json.Unmarshal([]byte(`{"memberIds":[0,-1,12345]}`), &req); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	got := req.MemberIDs.int64s()
	if len(got) != 1 || got[0] != 12345 {
		t.Fatalf("应只保留 12345, 实际 %v", got)
	}
}

// TestIDsOfPreservesOrder 验证 ID 列表转换保序且空输入返回 nil。
func TestIDsOfPreservesOrder(t *testing.T) {
	if got := idsOf(nil); got != nil {
		t.Errorf("空输入应返回 nil, 得到 %v", got)
	}

	in := []int64{snowflakeSample, 1, 2}
	got := idsOf(in)
	if len(got) != len(in) {
		t.Fatalf("长度 = %d, 期望 %d", len(got), len(in))
	}
	for i := range in {
		if int64(got[i]) != in[i] {
			t.Fatalf("第 %d 项 = %d, 期望 %d", i, got[i], in[i])
		}
	}
}
