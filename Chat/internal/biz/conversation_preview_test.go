package biz

import (
	"context"
	"io"
	"testing"

	klog "github.com/go-kratos/kratos/v2/log"
)

type previewRepo struct {
	ConversationRepo
	aliasCalls int
}

func (*previewRepo) ListByUser(context.Context, int64) ([]*UserConversation, error) {
	return []*UserConversation{
		{Conversation: &Conversation{ID: 1, Type: ConversationTypeSingle}, Member: &ConversationMember{}},
		{Conversation: &Conversation{ID: 2, Type: ConversationTypeGroup, Name: "一群"}, Member: &ConversationMember{AliasName: "我的昵称"}},
		{Conversation: &Conversation{ID: 3, Type: ConversationTypeGroup, Name: "二群"}, Member: &ConversationMember{}},
		{Conversation: &Conversation{ID: 4, Type: ConversationTypeGroup, Name: "空群"}, Member: &ConversationMember{}},
	}, nil
}
func (*previewRepo) FindPeerIDs(context.Context, []int64, int64) (map[int64]int64, error) {
	return map[int64]int64{1: 68}, nil
}
func (r *previewRepo) FindMemberAliases(_ context.Context, senders map[int64]int64) (map[int64]string, error) {
	r.aliasCalls++
	return map[int64]string{2: "乙的群昵称"}, nil
}

type previewMessages struct{ MessageRepo }

func (*previewMessages) FindLastByConversations(context.Context, []int64) (map[int64]*Message, error) {
	return map[int64]*Message{1: {SenderID: 68}, 2: {SenderID: 68}, 3: {SenderID: 71}}, nil
}

type previewUsers struct {
	calls int
	ids   []int64
}

func (u *previewUsers) BatchGetBriefs(_ context.Context, ids []int64) (map[int64]*UserBrief, error) {
	u.calls++
	u.ids = ids
	return map[int64]*UserBrief{68: {Nickname: "乙的个人昵称"}, 71: {Nickname: "丙的个人昵称"}}, nil
}
func TestConversationPreviewUsesSenderNicknameAndBatchesProfiles(t *testing.T) {
	repo, users := &previewRepo{}, &previewUsers{}
	uc := NewConversationUseCase(repo, &previewMessages{}, users, nil, klog.NewStdLogger(io.Discard))
	items, err := uc.ListConversations(context.Background(), 67)
	if err != nil {
		t.Fatal(err)
	}
	if items[1].LastSenderName != "乙的群昵称" || items[2].LastSenderName != "丙的个人昵称" || items[0].LastSenderName != "" || items[3].LastSenderName != "" {
		t.Fatalf("incorrect sender names: %+v", items)
	}
	if items[1].DisplayName != "一群" {
		t.Fatal("own group nickname replaced group name")
	}
	if users.calls != 1 || repo.aliasCalls != 1 || len(users.ids) != 2 {
		t.Fatal("preview introduced per-conversation profile lookups or duplicate IDs")
	}
}
