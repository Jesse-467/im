package biz

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	klog "github.com/go-kratos/kratos/v2/log"
)

type groupTestRepo struct {
	ConversationRepo
	typeID int32
	role   int32
	writes int
	patch  *GroupInfoPatch
}

func (r *groupTestRepo) FindByID(context.Context, int64) (*Conversation, error) {
	return &Conversation{Type: r.typeID}, nil
}
func (r *groupTestRepo) FindMember(context.Context, int64, int64) (*ConversationMember, error) {
	return &ConversationMember{UserID: 67, Role: r.role}, nil
}
func (r *groupTestRepo) ListMembers(context.Context, int64) ([]*ConversationMember, error) {
	return []*ConversationMember{{UserID: 67}}, nil
}
func (r *groupTestRepo) Create(context.Context, *Conversation, []*ConversationMember) error {
	r.writes++
	return nil
}
func (r *groupTestRepo) AddMembers(context.Context, int64, []*ConversationMember) (int, error) {
	r.writes++
	return 1, nil
}
func (r *groupTestRepo) RemoveMember(context.Context, int64, int64) error { r.writes++; return nil }
func (r *groupTestRepo) UpdateGroupInfo(_ context.Context, _ int64, _ int64, patch *GroupInfoPatch) error {
	r.writes++
	r.patch = patch
	return nil
}

type groupTestUsers struct{}

func (groupTestUsers) BatchGetBriefs(_ context.Context, ids []int64) (map[int64]*UserBrief, error) {
	result := map[int64]*UserBrief{}
	for _, id := range ids {
		if id == 67 || id == 68 || id == 71 {
			result[id] = &UserBrief{UserID: id}
		}
	}
	return result, nil
}
func groupTestUC(repo *groupTestRepo) *GroupUseCase {
	return NewGroupUseCase(repo, nil, groupTestUsers{}, &recallIDs{}, klog.NewStdLogger(io.Discard))
}
func stringPtr(value string) *string { return &value }

func TestGroupUpdateValidatesBeforeWriting(t *testing.T) {
	for _, test := range []struct {
		role  int32
		patch *GroupInfoPatch
		want  error
	}{
		{MemberRoleMember, &GroupInfoPatch{Name: stringPtr("越权修改"), AliasName: stringPtr("不能先保存这个昵称")}, ErrNoPermission},
		{MemberRoleOwner, &GroupInfoPatch{Name: stringPtr(" "), AliasName: stringPtr("不能先保存这个昵称")}, ErrInvalidParam},
		{MemberRoleOwner, &GroupInfoPatch{Name: stringPtr(strings.Repeat("群", 51)), AliasName: stringPtr("不能先保存这个昵称")}, ErrInvalidParam},
		{MemberRoleOwner, &GroupInfoPatch{AliasName: stringPtr(strings.Repeat("名", 33))}, ErrInvalidParam},
	} {
		repo := &groupTestRepo{typeID: ConversationTypeGroup, role: test.role}
		if err := groupTestUC(repo).UpdateGroupInfo(context.Background(), 1, 67, test.patch); !errors.Is(err, test.want) {
			t.Fatalf("err=%v want=%v", err, test.want)
		}
		if repo.writes != 0 {
			t.Fatal("failed patch changed state")
		}
	}
	repo := &groupTestRepo{typeID: ConversationTypeGroup, role: MemberRoleMember}
	if err := groupTestUC(repo).UpdateGroupInfo(context.Background(), 1, 67, &GroupInfoPatch{AliasName: stringPtr("")}); err != nil {
		t.Fatal(err)
	}
	if repo.patch.AliasName == nil || *repo.patch.AliasName != "" || repo.writes != 1 {
		t.Fatal("cannot restore default nickname")
	}
}

func TestGroupRejectsInvalidMembersAndSingleMutations(t *testing.T) {
	repo := &groupTestRepo{typeID: ConversationTypeGroup, role: MemberRoleOwner}
	uc := groupTestUC(repo)
	if _, _, err := uc.CreateGroup(context.Background(), 67, "测试群", []int64{999999}); !errors.Is(err, ErrUserNotFound) {
		t.Fatal(err)
	}
	if _, err := uc.AddMembers(context.Background(), 1, 67, []int64{999999}); !errors.Is(err, ErrUserNotFound) {
		t.Fatal(err)
	}
	if err := uc.QuitGroup(context.Background(), 1, 67); !errors.Is(err, ErrGroupOwnerCannotQuit) {
		t.Fatal(err)
	}
	if repo.writes != 0 {
		t.Fatal("invalid membership mutated state")
	}
	repo.typeID = ConversationTypeSingle
	if err := uc.QuitGroup(context.Background(), 1, 67); !errors.Is(err, ErrConversationTypeInvalid) {
		t.Fatal(err)
	}
	if _, err := uc.AddMembers(context.Background(), 1, 67, []int64{68}); !errors.Is(err, ErrConversationTypeInvalid) {
		t.Fatal(err)
	}
	if err := uc.UpdateGroupInfo(context.Background(), 1, 67, &GroupInfoPatch{Name: stringPtr("不能改名")}); !errors.Is(err, ErrConversationTypeInvalid) {
		t.Fatal(err)
	}
	if repo.writes != 0 {
		t.Fatal("single conversation was mutated as a group")
	}
}

func TestGroupNicknameDoesNotReplaceConversationName(t *testing.T) {
	rel := &UserConversation{Conversation: &Conversation{Type: ConversationTypeGroup, Name: "产品验收群"}, Member: &ConversationMember{AliasName: "我的群昵称"}}
	name, _ := resolveDisplay(rel, 0, nil)
	if name != "产品验收群" {
		t.Fatalf("group displayed as %q", name)
	}
}

type missingFriendRepo struct {
	FriendRepo
	writes int
}

func (*missingFriendRepo) AreFriends(context.Context, int64, int64) (bool, error) { return false, nil }
func (r *missingFriendRepo) CreateRequest(context.Context, *FriendRequest) error {
	r.writes++
	return nil
}
func TestFriendRejectsMissingUserBeforeCreatingRequest(t *testing.T) {
	repo := &missingFriendRepo{}
	uc := NewFriendUseCase(repo, nil, groupTestUsers{}, klog.NewStdLogger(io.Discard))
	if _, _, err := uc.ApplyFriend(context.Background(), 67, 999999, "测试"); !errors.Is(err, ErrUserNotFound) {
		t.Fatal(err)
	}
	if repo.writes != 0 {
		t.Fatal("missing user got a friend request")
	}
}
