// Package service 是业务用例对外的适配层。
//
// 职责边界：只做「参数绑定 → 调用 biz → 错误码映射 → 组装响应」。
// 任何业务判断都属于 biz 层，本层不得出现 if/else 形式的业务分支。
package service

import (
	"errors"

	klog "github.com/go-kratos/kratos/v2/log"

	"github.com/Jesse-467/im/Chat/internal/biz"
	"github.com/Jesse-467/im/Chat/internal/conf"
	"github.com/Jesse-467/im/Chat/internal/errs"
)

// ChatService 是即时聊天服务的实现。
//
// 当前只挂载 HTTP 传输层；后续接入 gRPC 与 WebSocket 网关时，
// 它们会复用同一份用例，因此业务语义不会在多个入口之间漂移。
type ChatService struct {
	convUC   *biz.ConversationUseCase
	friendUC *biz.FriendUseCase
	groupUC  *biz.GroupUseCase
	msgUC    *biz.MessageUseCase

	cfg *conf.Config
	log *klog.Helper
}

// NewChatService 构造服务实现。
func NewChatService(
	convUC *biz.ConversationUseCase,
	friendUC *biz.FriendUseCase,
	groupUC *biz.GroupUseCase,
	msgUC *biz.MessageUseCase,
	cfg *conf.Config,
	logger klog.Logger,
) *ChatService {
	return &ChatService{
		convUC:   convUC,
		friendUC: friendUC,
		groupUC:  groupUC,
		msgUC:    msgUC,
		cfg:      cfg,
		log:      klog.NewHelper(klog.With(logger, "module", "service/chat")),
	}
}

// toErrs 把领域错误翻译为对外错误码。
//
// 这是分层的关键收口点：biz 只表达「发生了什么」，由这里决定
// 「对外的错误码与文案是什么」。
func toErrs(err error) error {
	if err == nil {
		return nil
	}

	// 已经是带错误码的错误，直接透传（例如鉴权失败）
	var ce *errs.CodeError
	if errors.As(err, &ce) {
		return ce
	}

	code := bizErrCode(err)
	if code == 0 {
		// 未知错误（数据库、网络等）：仅记录，绝不把内部细节透出给客户端
		return errs.Wrap(err, errs.CodeServerError, "")
	}
	// 已知的领域错误：其文案是刻意写给用户看的，可以安全外露
	return errs.Wrap(err, code, err.Error())
}

// bizErrCode 把领域错误映射为业务错误码，未识别时返回 0。
func bizErrCode(err error) uint32 {
	switch {
	case errors.Is(err, biz.ErrInvalidParam):
		return errs.CodeParamError

	case errors.Is(err, biz.ErrNoPermission),
		errors.Is(err, biz.ErrNotGroupOwner),
		errors.Is(err, biz.ErrGroupOwnerCannotQuit),
		errors.Is(err, biz.ErrCannotRemoveOwner),
		errors.Is(err, biz.ErrNotConversationMember),
		errors.Is(err, biz.ErrNotFriend):
		return errs.CodeForbidden

	case errors.Is(err, biz.ErrConversationNotFound),
		errors.Is(err, biz.ErrFriendRequestNotFound),
		errors.Is(err, biz.ErrGroupNotFound),
		errors.Is(err, biz.ErrMessageNotFound):
		return errs.CodeNoData

	case errors.Is(err, biz.ErrAlreadyFriends),
		errors.Is(err, biz.ErrFriendRequestPending),
		errors.Is(err, biz.ErrFriendRequestHandled),
		errors.Is(err, biz.ErrAlreadyGroupMember),
		errors.Is(err, biz.ErrCannotAddSelf),
		errors.Is(err, biz.ErrMessageNotRecallable):
		return errs.CodeDataExist

	default:
		return 0
	}
}
