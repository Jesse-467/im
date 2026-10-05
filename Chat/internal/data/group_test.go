package data

import (
	"context"
	"os"
	"testing"

	"github.com/Jesse-467/im/Chat/internal/biz"
	gormpg "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 真实 PostgreSQL 的私有临时表验证重复邀请、退群/重入、人数与跨表原子性。
func TestGroupMembershipAndPatchTransactions(t *testing.T) {
	dsn := os.Getenv("CHAT_TEST_DSN")
	if dsn == "" {
		t.Skip("set CHAT_TEST_DSN to run PostgreSQL transaction test")
	}
	db, err := gorm.Open(gormpg.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("cannot connect to test PostgreSQL")
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	defer tx.Rollback()
	for _, query := range []string{
		`CREATE TEMP TABLE conversation (id bigint PRIMARY KEY, type integer, name text, avatar_url text, max_seq bigint, member_count integer, updated_at timestamptz) ON COMMIT DROP`,
		`CREATE TEMP TABLE conversation_member (id bigserial PRIMARY KEY, conversation_id bigint, user_id bigint, alias_name text CHECK(alias_name <> 'forced-failure'), role integer, last_read_seq bigint, unread_count bigint, mute integer DEFAULT 0, pinned integer DEFAULT 0, joined_at timestamptz, left_at timestamptz, UNIQUE(conversation_id,user_id)) ON COMMIT DROP`,
		`INSERT INTO conversation VALUES (1,2,'原群名','',100,1,now())`,
		`INSERT INTO conversation_member (conversation_id,user_id,alias_name,role,last_read_seq,unread_count,joined_at,left_at) VALUES (1,67,'',2,100,0,now(),NULL)`,
	} {
		if err := tx.Exec(query).Error; err != nil {
			t.Fatal(err)
		}
	}
	repo := &conversationRepo{data: &Data{db: tx}}
	ctx := context.Background()
	assertCount := func(want int64) {
		t.Helper()
		var active, stored int64
		if err := tx.Model(&conversationMemberModel{}).Where("conversation_id=1 AND left_at IS NULL").Count(&active).Error; err != nil {
			t.Fatal(err)
		}
		if err := tx.Raw(`SELECT member_count FROM conversation WHERE id=1`).Scan(&stored).Error; err != nil {
			t.Fatal(err)
		}
		if active != want || stored != want {
			t.Fatalf("active=%d stored=%d want=%d", active, stored, want)
		}
	}
	for i := 0; i < 2; i++ {
		added, err := repo.AddMembers(ctx, 1, []*biz.ConversationMember{{UserID: 68}, {UserID: 68}})
		if err != nil || added != 1-i {
			t.Fatalf("invite %d: added=%d err=%v", i, added, err)
		}
		assertCount(2)
	}
	if err := repo.RemoveMember(ctx, 1, 68); err != nil {
		t.Fatal(err)
	}
	assertCount(1)
	if err := repo.RemoveMember(ctx, 1, 68); err != nil {
		t.Fatal(err)
	}
	assertCount(1)
	if added, err := repo.AddMembers(ctx, 1, []*biz.ConversationMember{{UserID: 68}}); err != nil || added != 1 {
		t.Fatalf("rejoin %d %v", added, err)
	}
	assertCount(2)
	var member conversationMemberModel
	if err := tx.Where("user_id=68").First(&member).Error; err != nil {
		t.Fatal(err)
	}
	if member.LeftAt != nil || member.LastReadSeq != 100 || member.UnreadCount != 0 || member.Role != biz.MemberRoleMember {
		t.Fatalf("bad rejoin state: %+v", member)
	}
	name, alias := "新群名", "forced-failure"
	if err := repo.UpdateGroupInfo(ctx, 1, 67, &biz.GroupInfoPatch{Name: &name, AliasName: &alias}); err == nil {
		t.Fatal("expected member constraint failure")
	}
	var storedName string
	if err := tx.Raw(`SELECT name FROM conversation WHERE id=1`).Scan(&storedName).Error; err != nil {
		t.Fatal(err)
	}
	if storedName != "原群名" {
		t.Fatal("group name was not rolled back")
	}
	alias = "验收群昵称"
	if err := repo.UpdateGroupInfo(ctx, 1, 67, &biz.GroupInfoPatch{Name: &name, AliasName: &alias}); err != nil {
		t.Fatal(err)
	}
	member = conversationMemberModel{}
	if err := tx.Where("user_id=67").First(&member).Error; err != nil {
		t.Fatal(err)
	}
	if member.AliasName != alias {
		t.Fatal("nickname not saved")
	}
	aliases, err := repo.FindMemberAliases(ctx, map[int64]int64{1: 67})
	if err != nil || aliases[1] != alias {
		t.Fatalf("last sender aliases=%v err=%v", aliases, err)
	}
	var waterMark int64
	if err := tx.Raw(`SELECT max_seq FROM conversation WHERE id=1`).Scan(&waterMark).Error; err != nil {
		t.Fatal(err)
	}
	if waterMark != 100 {
		t.Fatal("group patch changed message cursor")
	}
	alias = ""
	if err := repo.UpdateGroupInfo(ctx, 1, 67, &biz.GroupInfoPatch{AliasName: &alias}); err != nil {
		t.Fatal(err)
	}
	// PostgreSQL 上验证并发入口的数据库级容量校验，不能只依赖业务层先查。
	if err := tx.Exec(`INSERT INTO conversation_member (conversation_id,user_id,alias_name,role,last_read_seq,unread_count,joined_at,left_at) SELECT 1,n,'',0,100,0,now(),NULL FROM generate_series(1000,1497) AS n`).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AddMembers(ctx, 1, []*biz.ConversationMember{{UserID: 71}}); err != biz.ErrGroupMemberLimitExceeded {
		t.Fatalf("capacity error=%v", err)
	}
}
