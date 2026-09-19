package data

import (
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	"github.com/Jesse-467/im/Chat/internal/biz"
)

// pgUniqueViolation 是 PostgreSQL 唯一约束冲突的 SQLSTATE 错误码。
//
// 直接比对标准错误码而未引入官方常量包：该码由 SQL 标准固定，极少变化，
// 少一个依赖更利于长期维护。
const pgUniqueViolation = "23505"

// normalize 把「记录不存在」归一化为业务错误，其余错误统一包装。
//
// 这一层归一化是刻意的：biz 层因此不需要 import gorm，也不会因为更换 ORM
// 或改用云数据库 SDK 被牵连；notFound 由各仓储指定为对应的领域错误。
func normalize(err error, notFound error, action string) error {
	if isRecordNotFound(err) {
		return notFound
	}
	return fmt.Errorf("data: %s失败: %w", action, err)
}

// isRecordNotFound 判断错误是否表示「记录不存在」。
func isRecordNotFound(err error) bool {
	return errors.Is(err, gorm.ErrRecordNotFound)
}

// isDuplicateKey 判断是否为唯一约束冲突。
func isDuplicateKey(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation
}

// nullableTime 把 Go 的零值时间转换为 NULL。
//
// PostgreSQL 的 TIMESTAMPTZ 虽然能容纳 0001-01-01，但把「未处理」写成该日期
// 会让后续按时间过滤的查询产生歧义，因此统一落成 NULL 表达「无此时间」。
func nullableTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// nullableJSON 把空字符串转换为 NULL。
//
// JSONB 列不接受空串（” 不是合法 JSON），因此承载 JSON 的字段一律用 *string：
// 空串代表「无扩展内容」，落库为 NULL。
func nullableJSON(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// derefString 把可空字符串还原为业务层的空串语义。
func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ── 会话 ────────────────────────────────────────────────────────────────────

// conversationModel 是 conversation 表的 ORM 映射。
//
// 字段与 migrations/0001_init.up.sql 严格对应；业务实体由 biz.Conversation 表达，
// 两者分离可以让表结构演进不直接冲击业务层。
type conversationModel struct {
	// autoIncrement:false 是必须的：该列是雪花 ID，由应用在写入前生成，
	// 若让 GORM 误判为自增，插入语句会省略 id 并依赖 RETURNING，落库必然失败。
	ID          int64  `gorm:"column:id;primaryKey;autoIncrement:false"`
	Type        int32  `gorm:"column:type;not null"`
	BizKey      string `gorm:"column:biz_key;size:191;not null"`
	Name        string `gorm:"column:name;size:191;not null;default:''"`
	AvatarURL   string `gorm:"column:avatar_url;size:512;not null;default:''"`
	Status      int32  `gorm:"column:status;not null;default:1"`
	OwnerID     int64  `gorm:"column:owner_id;not null;default:0"`
	MaxSeq      int64  `gorm:"column:max_seq;not null;default:0"`
	MemberCount int32  `gorm:"column:member_count;not null;default:0"`
	// Extra 是会话维度的扩展 JSON，当前业务尚未使用，保留以对齐表结构。
	Extra     *string   `gorm:"column:extra;type:jsonb"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"`
	// updated_at 由 GORM 在每次写入时维护：PostgreSQL 没有 MySQL 的
	// ON UPDATE CURRENT_TIMESTAMP，必须由应用侧保证该列被刷新。
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime"`
}

// TableName 显式指定表名，不依赖 GORM 的复数化推断。
func (conversationModel) TableName() string { return "conversation" }

// userConversationRow 是「用户参与的会话」联表查询的投影行。
//
// 单独定义而不是复用 conversationModel：这次查询把 conversation 与
// conversation_member 的列拍平到同一行，字段与 select 子句一一对应更不易出错。
type userConversationRow struct {
	ID        int64     `gorm:"column:id"`
	Type      int32     `gorm:"column:type"`
	BizKey    string    `gorm:"column:biz_key"`
	Name      string    `gorm:"column:name"`
	AvatarURL string    `gorm:"column:avatar_url"`
	Status    int32     `gorm:"column:status"`
	OwnerID   int64     `gorm:"column:owner_id"`
	MaxSeq    int64     `gorm:"column:max_seq"`
	CreatedAt time.Time `gorm:"column:created_at"`

	AliasName   string    `gorm:"column:alias_name"`
	Role        int32     `gorm:"column:role"`
	LastReadSeq int64     `gorm:"column:last_read_seq"`
	UnreadCount int64     `gorm:"column:unread_count"`
	JoinedAt    time.Time `gorm:"column:joined_at"`
}

// toBizConversation 把存储模型转换为业务实体。
func toBizConversation(m *conversationModel) *biz.Conversation {
	return &biz.Conversation{
		ID:          m.ID,
		Type:        m.Type,
		BizKey:      m.BizKey,
		Name:        m.Name,
		AvatarURL:   m.AvatarURL,
		Status:      m.Status,
		OwnerID:     m.OwnerID,
		MaxSeq:      m.MaxSeq,
		MemberCount: m.MemberCount,
		CreatedAt:   m.CreatedAt,
	}
}

// fromBizConversation 把业务实体转换为存储模型。
func fromBizConversation(c *biz.Conversation) *conversationModel {
	return &conversationModel{
		ID:          c.ID,
		Type:        c.Type,
		BizKey:      c.BizKey,
		Name:        c.Name,
		AvatarURL:   c.AvatarURL,
		Status:      c.Status,
		OwnerID:     c.OwnerID,
		MaxSeq:      c.MaxSeq,
		MemberCount: c.MemberCount,
		CreatedAt:   c.CreatedAt,
	}
}

// ── 会话成员 ────────────────────────────────────────────────────────────────

// conversationMemberModel 是 conversation_member 表的 ORM 映射。
type conversationMemberModel struct {
	ID             int64  `gorm:"column:id;primaryKey;autoIncrement"`
	ConversationID int64  `gorm:"column:conversation_id;not null"`
	UserID         int64  `gorm:"column:user_id;not null"`
	AliasName      string `gorm:"column:alias_name;size:191;not null;default:''"`
	Role           int32  `gorm:"column:role;not null;default:0"`
	LastReadSeq    int64  `gorm:"column:last_read_seq;not null;default:0"`
	UnreadCount    int64  `gorm:"column:unread_count;not null;default:0"`
	Mute           int32  `gorm:"column:mute;not null;default:0"`
	Pinned         int32  `gorm:"column:pinned;not null;default:0"`
	// joined_at 在插入时由 GORM 写入，群成员列表要按它排序。
	JoinedAt time.Time `gorm:"column:joined_at;autoCreateTime"`
	// left_at 用指针表达可空：nil 表示仍在会话中。
	LeftAt *time.Time `gorm:"column:left_at"`
}

// TableName 显式指定表名。
func (conversationMemberModel) TableName() string { return "conversation_member" }

// toBizMember 把存储模型转换为业务实体。
func toBizMember(m *conversationMemberModel) *biz.ConversationMember {
	return &biz.ConversationMember{
		ConversationID: m.ConversationID,
		UserID:         m.UserID,
		AliasName:      m.AliasName,
		Role:           m.Role,
		LastReadSeq:    m.LastReadSeq,
		UnreadCount:    m.UnreadCount,
		JoinedAt:       m.JoinedAt,
		LeftAt:         m.LeftAt,
	}
}

// fromBizMember 把业务实体转换为存储模型。
//
// 调用方负责补齐 ConversationID：它属于「挂到哪个会话」这一上下文信息，
// 而业务实体在创建单个会话时并不持有它。
func fromBizMember(m *biz.ConversationMember) *conversationMemberModel {
	return &conversationMemberModel{
		ConversationID: m.ConversationID,
		UserID:         m.UserID,
		AliasName:      m.AliasName,
		Role:           m.Role,
		LastReadSeq:    m.LastReadSeq,
		UnreadCount:    m.UnreadCount,
		JoinedAt:       m.JoinedAt,
		LeftAt:         m.LeftAt,
	}
}

// ── 消息 ────────────────────────────────────────────────────────────────────

// messageModel 是 message 表的 ORM 映射。
type messageModel struct {
	// 消息 ID 同样是应用侧生成的雪花值，不能让 GORM 当作自增处理。
	ID             int64 `gorm:"column:id;primaryKey;autoIncrement:false"`
	ConversationID int64 `gorm:"column:conversation_id;not null"`
	Seq            int64 `gorm:"column:seq;not null"`
	// SenderSeq 是发送者在本会话内的序号。
	// 它是数据库级幂等的关键：与 client_msg_id 一起构成两条唯一约束，
	// 使客户端换 ID 重试也无法绕过。
	SenderSeq   int64   `gorm:"column:sender_seq;not null"`
	SenderID    int64   `gorm:"column:sender_id;not null"`
	Type        int32   `gorm:"column:type;not null;default:1"`
	Content     string  `gorm:"column:content;type:text"`
	Extra       *string `gorm:"column:extra;type:jsonb"`
	ClientMsgID string  `gorm:"column:client_msg_id;size:64;not null"`
	Status      int32   `gorm:"column:status;not null;default:1"`
	// recalled_at / recalled_by 记录撤回操作，用于合规追溯。
	// 用指针表达可空：未撤回时为 NULL，而非零值时间。
	RecalledAt *time.Time `gorm:"column:recalled_at"`
	RecalledBy int64      `gorm:"column:recalled_by;not null;default:0"`
	CreatedAt  time.Time  `gorm:"column:created_at;autoCreateTime"`
}

// TableName 显式指定表名。
func (messageModel) TableName() string { return "message" }

// toBizMessage 把存储模型转换为业务实体。
func toBizMessage(m *messageModel) *biz.Message {
	msg := &biz.Message{
		ID:             m.ID,
		ConversationID: m.ConversationID,
		Seq:            m.Seq,
		SenderSeq:      m.SenderSeq,
		SenderID:       m.SenderID,
		Type:           m.Type,
		Content:        m.Content,
		Extra:          derefString(m.Extra),
		ClientMsgID:    m.ClientMsgID,
		Status:         m.Status,
		RecalledBy:     m.RecalledBy,
		CreatedAt:      m.CreatedAt,
	}
	if m.RecalledAt != nil {
		msg.RecalledAt = *m.RecalledAt
	}
	return msg
}

// fromBizMessage 把业务实体转换为存储模型。
func fromBizMessage(m *biz.Message) *messageModel {
	out := &messageModel{
		ID:             m.ID,
		ConversationID: m.ConversationID,
		Seq:            m.Seq,
		SenderSeq:      m.SenderSeq,
		SenderID:       m.SenderID,
		Type:           m.Type,
		Content:        m.Content,
		Extra:          nullableJSON(m.Extra),
		ClientMsgID:    m.ClientMsgID,
		Status:         m.Status,
		RecalledBy:     m.RecalledBy,
		CreatedAt:      m.CreatedAt,
	}
	// 零值时间表示「未撤回」，写成 NULL 而不是 0001-01-01
	if !m.RecalledAt.IsZero() {
		out.RecalledAt = &m.RecalledAt
	}
	return out
}

// ── 好友关系 ────────────────────────────────────────────────────────────────

// friendRelationModel 是 friend_relation 表的 ORM 映射。
type friendRelationModel struct {
	ID        int64     `gorm:"column:id;primaryKey;autoIncrement"`
	UserID    int64     `gorm:"column:user_id;not null"`
	FriendID  int64     `gorm:"column:friend_id;not null"`
	Remark    string    `gorm:"column:remark;size:191;not null;default:''"`
	Status    int32     `gorm:"column:status;not null;default:1"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"`
}

// TableName 显式指定表名。
func (friendRelationModel) TableName() string { return "friend_relation" }

// toBizFriendRelation 把存储模型转换为业务实体。
func toBizFriendRelation(m *friendRelationModel) *biz.FriendRelation {
	return &biz.FriendRelation{
		UserID:    m.UserID,
		FriendID:  m.FriendID,
		Remark:    m.Remark,
		Status:    m.Status,
		CreatedAt: m.CreatedAt,
	}
}

// fromBizFriendRelation 把业务实体转换为存储模型。
func fromBizFriendRelation(r *biz.FriendRelation) *friendRelationModel {
	return &friendRelationModel{
		UserID:    r.UserID,
		FriendID:  r.FriendID,
		Remark:    r.Remark,
		Status:    r.Status,
		CreatedAt: r.CreatedAt,
	}
}

// ── 好友申请 ────────────────────────────────────────────────────────────────

// friendRequestModel 是 friend_request 表的 ORM 映射。
type friendRequestModel struct {
	ID       int64  `gorm:"column:id;primaryKey;autoIncrement"`
	FromUID  int64  `gorm:"column:from_uid;not null"`
	ToUID    int64  `gorm:"column:to_uid;not null"`
	ApplyMsg string `gorm:"column:apply_msg;size:255;not null;default:''"`
	Status   int32  `gorm:"column:status;not null;default:0"`
	// 申请必须带过期时间，用于让出 (from, to, status) 的唯一键。
	ExpireAt time.Time `gorm:"column:expire_at;not null"`
	// handled_at 可空：未处理的申请没有处理时间，用 NULL 而非零值时间表达。
	HandledAt *time.Time `gorm:"column:handled_at"`
	CreatedAt time.Time  `gorm:"column:created_at;autoCreateTime"`
}

// TableName 显式指定表名。
func (friendRequestModel) TableName() string { return "friend_request" }

// toBizFriendRequest 把存储模型转换为业务实体。
func toBizFriendRequest(m *friendRequestModel) *biz.FriendRequest {
	req := &biz.FriendRequest{
		ID:        m.ID,
		FromUID:   m.FromUID,
		ToUID:     m.ToUID,
		ApplyMsg:  m.ApplyMsg,
		Status:    m.Status,
		ExpireAt:  m.ExpireAt,
		CreatedAt: m.CreatedAt,
	}
	if m.HandledAt != nil {
		req.HandledAt = *m.HandledAt
	}
	return req
}

// fromBizFriendRequest 把业务实体转换为存储模型。
func fromBizFriendRequest(r *biz.FriendRequest) *friendRequestModel {
	return &friendRequestModel{
		ID:        r.ID,
		FromUID:   r.FromUID,
		ToUID:     r.ToUID,
		ApplyMsg:  r.ApplyMsg,
		Status:    r.Status,
		ExpireAt:  r.ExpireAt,
		HandledAt: nullableTime(r.HandledAt),
	}
}
