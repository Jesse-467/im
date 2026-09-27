package biz

import (
	"context"
	"io"
	"testing"

	klog "github.com/go-kratos/kratos/v2/log"
)

type pagingConvRepo struct{ ConversationRepo }

func (pagingConvRepo) FindMember(context.Context, int64, int64) (*ConversationMember, error) {
	return &ConversationMember{UserID: 67}, nil
}
func (pagingConvRepo) FindByID(context.Context, int64) (*Conversation, error) {
	return &Conversation{ID: 1, MaxSeq: 120}, nil
}

type pagingMessageRepo struct{ MessageRepo }

func (pagingMessageRepo) ListBySeqRange(_ context.Context, _ int64, from, to int64, limit int, ascending bool) ([]*Message, error) {
	var result []*Message
	for i := int64(1); i <= 120 && len(result) < limit; i++ {
		seq := i
		if !ascending {
			seq = 121 - i
		}
		if seq > from && (to == 0 || seq <= to) {
			result = append(result, &Message{ID: seq, Seq: seq})
		}
	}
	return result, nil
}

func TestPullSyncDoesNotSkipPages(t *testing.T) {
	uc := NewMessageUseCase(pagingMessageRepo{}, pagingConvRepo{}, nil, nil, nil, "", klog.NewStdLogger(io.Discard))
	cursor := int64(0)
	var received []*Message
	for page, want := range []int64{50, 100, 120} {
		result, err := uc.Pull(context.Background(), &PullMessagesRequest{
			ConversationID: 1, UserID: 67, FromSeq: cursor, Limit: 50, Ascending: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.MaxSeq != want || result.HasMore != (page < 2) {
			t.Fatalf("page %d: cursor=%d hasMore=%v", page, result.MaxSeq, result.HasMore)
		}
		cursor = result.MaxSeq
		received = append(received, result.Messages...)
	}
	if len(received) != 120 {
		t.Fatalf("received %d of 120 messages", len(received))
	}
	for i, msg := range received {
		if msg.Seq != int64(i+1) {
			t.Fatalf("missing message %d: got %d", i+1, msg.Seq)
		}
	}
}

func TestPullHistoryAndEmptyPageCursors(t *testing.T) {
	uc := NewMessageUseCase(pagingMessageRepo{}, pagingConvRepo{}, nil, nil, nil, "", klog.NewStdLogger(io.Discard))
	for _, req := range []*PullMessagesRequest{
		{ConversationID: 1, UserID: 67, ToSeq: 70, Limit: 50},
		{ConversationID: 1, UserID: 67, FromSeq: 120, Limit: 50, Ascending: true},
	} {
		result, err := uc.Pull(context.Background(), req)
		if err != nil || result.MaxSeq != 120 {
			t.Fatalf("result=%v err=%v", result, err)
		}
	}
}
