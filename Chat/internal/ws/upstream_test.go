package ws

import (
	"encoding/json"
	"testing"
)

// TestUpstreamMessageNumericID 验证数字形式的会话 ID 正常解码。
func TestUpstreamMessageNumericID(t *testing.T) {
	var m UpstreamMessage
	if err := json.Unmarshal([]byte(`{"type":"send","conversationId":359566823268454400,"content":"hi"}`), &m); err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	if m.ConversationID != 359566823268454400 {
		t.Fatalf("ConversationID = %d", m.ConversationID)
	}
	id, err := m.ResolveConversationID()
	if err != nil || id != 359566823268454400 {
		t.Fatalf("Resolve = %d, %v", id, err)
	}
}

// TestUpstreamMessageStringID 验证字符串形式的会话 ID 被兼容。
//
// 这是前端精度问题的兜底：19 位雪花 ID 超过 JS 的 Number.MAX_SAFE_INTEGER，
// 浏览器把 ID 当字符串回传时必须能正常解码，否则发送会以
// "消息格式不正确" 失败，而该问题只在 ID 足够大时才出现。
func TestUpstreamMessageStringID(t *testing.T) {
	var m UpstreamMessage
	if err := json.Unmarshal([]byte(`{"type":"send","conversationId":"359566823268454400","content":"hi"}`), &m); err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	if m.ConversationIDStr != "359566823268454400" {
		t.Fatalf("ConversationIDStr = %q", m.ConversationIDStr)
	}
	id, err := m.ResolveConversationID()
	if err != nil || id != 359566823268454400 {
		t.Fatalf("Resolve = %d, %v", id, err)
	}
}

// TestUpstreamMessageInvalidID 验证非法字符串被拒绝而不是静默当成 0。
func TestUpstreamMessageInvalidID(t *testing.T) {
	var m UpstreamMessage
	if err := json.Unmarshal([]byte(`{"type":"send","conversationId":"abc"}`), &m); err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	if _, err := m.ResolveConversationID(); err == nil {
		t.Fatal("非法会话 ID 应返回错误")
	}
}

// TestUpstreamMessageGroupIDFallback 验证未提供会话主键时回退到 groupId。
func TestUpstreamMessageGroupIDFallback(t *testing.T) {
	var m UpstreamMessage
	if err := json.Unmarshal([]byte(`{"type":"send","groupId":"S:1:2"}`), &m); err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	id, err := m.ResolveConversationID()
	if err != nil || id != 0 {
		t.Fatalf("无会话主键时应返回 0, 得到 %d, %v", id, err)
	}
	if m.GroupID != "S:1:2" {
		t.Fatalf("GroupID = %q", m.GroupID)
	}
}
