package biz

import (
	"context"
	"errors"
	klog "github.com/go-kratos/kratos/v2/log"
	"io"
	"testing"
)

type recallRepo struct {
	MessageRepo
	original *Message
	notice   *Message
	event    *OutboxEvent
	calls    int
}

func (r *recallRepo) FindByID(context.Context, int64, int64) (*Message, error) {
	return r.original, nil
}
func (r *recallRepo) Recall(_ context.Context, _ int64, _ int64, _ int64, notice *Message, event *OutboxEvent) (bool, error) {
	r.calls++
	r.notice = notice
	r.event = event
	r.original.Status = MessageStatusRecalled
	return true, nil
}

type recallSeq struct {
	SeqAllocator
	calls int
}

func (s *recallSeq) Next(context.Context, int64) (int64, error)               { s.calls++; return 121, nil }
func (*recallSeq) NextSenderSeq(context.Context, int64, int64) (int64, error) { return 5, nil }

type recallIDs struct{ next int64 }

func (g *recallIDs) Next() (int64, error) { g.next++; return g.next, nil }

func TestRecallCreatesOrderedEventAndIsIdempotent(t *testing.T) {
	repo := &recallRepo{original: &Message{ID: 10, SenderID: 67, Seq: 1, Type: MessageTypeText, Status: MessageStatusNormal}}
	seq := &recallSeq{}
	uc := NewMessageUseCase(repo, pagingConvRepo{}, nil, seq, &recallIDs{next: 1000}, "chat", klog.NewStdLogger(io.Discard))
	for i := 0; i < 2; i++ {
		if err := uc.Recall(context.Background(), 1, 10, 67); err != nil {
			t.Fatal(err)
		}
	}
	if repo.calls != 1 || seq.calls != 1 {
		t.Fatalf("duplicate recall: writes=%d allocations=%d", repo.calls, seq.calls)
	}
	if repo.notice.Type != MessageTypeRecall || repo.notice.Seq != 121 || repo.notice.RecallTargetID() != 10 || repo.event == nil {
		t.Fatalf("notice=%+v event=%+v", repo.notice, repo.event)
	}
	if err := uc.validateSendRequest(&SendMessageRequest{ConversationID: 1, SenderID: 67, Type: MessageTypeRecall, Content: "fake"}); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("client can forge recall: %v", err)
	}
}

func TestRecallRejectsAnotherSenderAndRecallEvent(t *testing.T) {
	for _, original := range []*Message{nil, {ID: 10, SenderID: 68, Type: MessageTypeText, Status: MessageStatusNormal}, {ID: 10, SenderID: 67, Type: MessageTypeRecall, Status: MessageStatusNormal}} {
		repo := &recallRepo{original: original}
		seq := &recallSeq{}
		uc := NewMessageUseCase(repo, pagingConvRepo{}, nil, seq, &recallIDs{}, "chat", klog.NewStdLogger(io.Discard))
		if err := uc.Recall(context.Background(), 1, 10, 67); !errors.Is(err, ErrMessageNotRecallable) {
			t.Fatalf("original=%+v err=%v", original, err)
		}
		if repo.calls != 0 || seq.calls != 0 {
			t.Fatal("invalid recall changed state")
		}
	}
}
