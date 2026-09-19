/**
 * 与后端 HTTP DTO 一一对应的类型定义。
 *
 * 字段命名与小驼峰契约保持一致（见 Account/Chat internal/service 下的 DTO）。
 */

export interface ApiBody<T> {
  code: number
  msg: string
  data: T
}

// ── Account 服务 ────────────────────────────────────────────────────────────

export interface Profile {
  userId: number
  nickName: string
  gender: number // 0 保密 1 男 2 女
  email: string
  avatarUrl: string
}

export interface LoginResult {
  userId: number
  accessToken: string
  accessExpire: number
}

// ── Chat 服务 ───────────────────────────────────────────────────────────────

export interface ChatMsg {
  id: number
  conversationId: number
  groupId: string
  seq: number
  senderId: number
  type: number // 1 文本
  content: string
  uuid: string
  createTime: number
}

/** 会话列表项（对齐 conversationItemDTO） */
export interface Conversation {
  conversationId: number
  groupId: string
  type: number // 1 单聊 2 群聊
  name: string
  aliasName: string
  avatarUrl: string
  unreadCount: number
  lastReadSeq: number
  maxSeq: number
  lastMsg: ChatMsg | null
}

export interface Friend {
  userId: number
  nickName: string
  avatarUrl: string
  remark: string
}

/** 好友申请：status 0 待处理 1 已同意 2 已拒绝 3 已过期 */
export interface FriendRequest {
  id: number
  fromUid: number
  toUid: number
  applyMsg: string
  status: number
  createdAt: number
  handledAt: number
}

export interface GroupMember {
  userId: number
  nickName: string
  avatarUrl: string
  aliasName: string
  role: number // 0 成员 1 管理员 2 群主
  lastReadSeq: number
  joinedAt: number
}

export interface PullResult {
  list: ChatMsg[]
  hasMore: boolean
  maxSeq: number
}

export interface SendResult {
  id: number
  conversationId: number
  groupId: string
  seq: number
  createTime: number
  duplicated: boolean
}

export interface HandleFriendResult {
  conversationId: number
  groupId: string
}

export interface CreateGroupResult {
  conversationId: number
  groupId: string
  addedCount: number
}

/** WebSocket 下行帧（对齐 ws.Message） */
export interface WsFrame<T = unknown> {
  type: 'message' | 'pong' | 'error'
  data: T
}

/** 发送回执（sendAckPayload），也用于新消息推送信令 */
export interface WsMessagePayload {
  conversationId: number
  messageId: number
  seq: number
  clientMsgId: string
  duplicated: boolean
  createTime: number
}
