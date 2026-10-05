package data

import (
	"context"
	"github.com/Jesse-467/im/Chat/internal/biz"
	gormpg "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"os"
	"testing"
	"time"
)

// 显式启用真实 PostgreSQL 测试；所有写入均为连接私有临时表，结束时回滚。
func TestRecallTransactionAtomicity(t *testing.T) {
	dsn := os.Getenv("CHAT_TEST_DSN")
	if dsn == "" {
		t.Skip("set CHAT_TEST_DSN to run PostgreSQL transaction test")
	}
	db, err := gorm.Open(gormpg.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("cannot connect to test PostgreSQL")
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	defer tx.Rollback()
	for _, query := range []string{
		`CREATE TEMP TABLE message (id bigint PRIMARY KEY, conversation_id bigint, seq bigint, sender_seq bigint, sender_id bigint, type integer, content text, extra jsonb, client_msg_id text, status integer, recalled_at timestamptz, recalled_by bigint, created_at timestamptz) ON COMMIT DROP`,
		`CREATE TEMP TABLE conversation (id bigint PRIMARY KEY, max_seq bigint, updated_at timestamptz) ON COMMIT DROP`,
		`CREATE TEMP TABLE conversation_member (conversation_id bigint, user_id bigint, left_at timestamptz, unread_count bigint) ON COMMIT DROP`,
		`CREATE TEMP TABLE message_outbox (id bigserial PRIMARY KEY, event_id text UNIQUE, topic text, partition_key text, payload bytea, status smallint, retry_count integer, last_error text, next_retry_at timestamptz, created_at timestamptz, updated_at timestamptz) ON COMMIT DROP`,
		`INSERT INTO message (id,conversation_id,seq,sender_id,type,status) VALUES (10,1,1,67,1,1)`,
		`INSERT INTO conversation VALUES (1,1,now())`,
		`INSERT INTO conversation_member VALUES (1,67,NULL,0),(1,68,NULL,0)`,
		`INSERT INTO message_outbox (event_id) VALUES ('duplicate')`,
	} {
		if err := tx.Exec(query).Error; err != nil {
			t.Fatal(err)
		}
	}
	repo := &messageRepo{data: &Data{db: tx}}
	notice := &biz.Message{ID: 11, ConversationID: 1, Seq: 2, SenderSeq: 2, SenderID: 67, Type: biz.MessageTypeRecall, Status: 1, Content: "撤回了一条消息", Extra: `{"recallMessageId":"10"}`, ClientMsgID: "recall-11", CreatedAt: time.Now()}
	event := &biz.OutboxEvent{EventID: "duplicate", Topic: "chat", PartitionKey: "1", Payload: []byte(`{}`)}
	if ok, err := repo.Recall(context.Background(), 1, 10, 67, notice, event); err == nil || ok {
		t.Fatal("expected outbox constraint failure")
	}
	original, err := repo.FindByID(context.Background(), 1, 10)
	if err != nil || original.Status != 1 {
		t.Fatalf("original was not rolled back: %+v %v", original, err)
	}
	var count int64
	tx.Model(&messageModel{}).Where("id = 11").Count(&count)
	if count != 0 {
		t.Fatal("notice was not rolled back")
	}
	var conv conversationModel
	tx.First(&conv, 1)
	if conv.MaxSeq != 1 {
		t.Fatal("watermark was not rolled back")
	}
	var unread int64
	tx.Raw(`SELECT unread_count FROM conversation_member WHERE user_id = 68`).Scan(&unread)
	if unread != 0 {
		t.Fatal("unread was not rolled back")
	}
	event.EventID = "success"
	if ok, err := repo.Recall(context.Background(), 1, 10, 67, notice, event); err != nil || !ok {
		t.Fatalf("recall failed: %v %v", ok, err)
	}
	if ok, err := repo.Recall(context.Background(), 1, 10, 67, notice, event); err != nil || ok {
		t.Fatalf("recall duplicated: %v %v", ok, err)
	}
	original, err = repo.FindByID(context.Background(), 1, 10)
	if err != nil || original.Status != 2 {
		t.Fatal("original not recalled")
	}
	tx.Model(&messageModel{}).Where("type = 6").Count(&count)
	if count != 1 {
		t.Fatalf("notice count=%d", count)
	}
	tx.Raw(`SELECT unread_count FROM conversation_member WHERE user_id = 68`).Scan(&unread)
	if unread != 1 {
		t.Fatalf("unread=%d", unread)
	}
	tx.Model(&messageOutboxModel{}).Where("event_id = 'success'").Count(&count)
	if count != 1 {
		t.Fatal("missing outbox")
	}
}
