import { post } from './client'
import type {
  ChatMsg,
  Conversation,
  CreateGroupResult,
  Friend,
  FriendRequest,
  GroupMember,
  HandleFriendResult,
  PullResult,
  SendResult
} from './types'

/** Chat 服务 API（默认 http://127.0.0.1:8002） */

// ── 会话 ──

export function apiConversationList(): Promise<{ list: Conversation[] }> {
  return post('chat', '/api/group/message_group_info_list', {})
}

export function apiMarkRead(conversationId: number, seq: number): Promise<{ success: boolean }> {
  return post('chat', '/api/group/mark_read', { conversationId, seq })
}

// ── 消息 ──

export function apiSendMessage(
  conversationId: number,
  content: string,
  clientMsgId: string
): Promise<SendResult> {
  return post('chat', '/api/message/upload', {
    conversationId,
    type: 1,
    content,
    clientMsgId
  })
}

export function apiPullMessages(conversationId: number, limit: number): Promise<PullResult> {
  return post('chat', '/api/message/pull', { conversationId, limit })
}

export function apiSyncMessages(
  conversationId: number,
  fromSeq: number,
  limit: number
): Promise<PullResult> {
  return post('chat', '/api/message/sync', { conversationId, fromSeq, limit })
}

export function apiRecallMessage(
  conversationId: number,
  messageId: number
): Promise<{ success: boolean }> {
  return post('chat', '/api/message/recall', { conversationId, messageId })
}

// ── 好友 ──

export function apiAddFriend(
  userId: number,
  applyMsg: string
): Promise<{ requestId: number; alreadyFriends: boolean }> {
  return post('chat', '/api/group/add_friend', { userId, applyMsg })
}

export function apiHandleFriend(requestId: number, isAgree: boolean): Promise<HandleFriendResult> {
  return post('chat', '/api/group/handle_friend', { requestId, isAgree })
}

export function apiFriendList(): Promise<{ list: Friend[] }> {
  return post('chat', '/api/friend/list', {})
}

export function apiFriendRequestList(
  status?: number
): Promise<{ list: FriendRequest[] }> {
  return post('chat', '/api/friend/request_list', status === undefined ? {} : { status })
}

// ── 群聊 ──

export function apiCreateGroupChat(
  groupName: string,
  memberIds: number[]
): Promise<CreateGroupResult> {
  return post('chat', '/api/group/create_group_chat', { groupName, memberIds })
}

export function apiGroupMemberList(conversationId: number): Promise<{ list: GroupMember[] }> {
  return post('chat', '/api/group/member_list', { conversationId })
}

export function apiGroupUserList(conversationId: number): Promise<{ list: number[] }> {
  return post('chat', '/api/group/group_user_list', { conversationId })
}

export function apiQuitGroup(conversationId: number): Promise<{ success: boolean }> {
  return post('chat', '/api/group/quit', { conversationId })
}

// ── 工具 ──

/** 兼容旧接口的空载映射（保留给未来按 uuid 查询等场景） */
export function toChatMsgMap(list: ChatMsg[]): Map<string, ChatMsg> {
  const map = new Map<string, ChatMsg>()
  for (const m of list) {
    map.set(m.uuid || String(m.id), m)
  }
  return map
}
