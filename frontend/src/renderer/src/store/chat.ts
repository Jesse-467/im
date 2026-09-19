import { create } from 'zustand'
import {
  apiAddFriend,
  apiConversationList,
  apiCreateGroupChat,
  apiFriendList,
  apiFriendRequestList,
  apiGroupMemberList,
  apiGroupUserList,
  apiHandleFriend,
  apiMarkRead,
  apiPullMessages,
  apiQuitGroup,
  apiRecallMessage,
  apiSendMessage,
  apiSyncMessages
} from '@/api/chat'
import { ApiError, CODE_TOKEN_REVOKED, CODE_UNAUTHORIZED } from '@/api/client'
import { getToken } from '@/core/session'
import { wsClient, type WsStatus } from '@/core/ws'
import type { Conversation, Friend, FriendRequest, GroupMember, WsMessagePayload } from '@/api/types'
import { useAuthStore } from './auth'
import { toast } from './toast'

/** 本地消息：服务端消息 + 发送中间态（pending/failed/recalled） */
export interface LocalMsg {
  id: string // pending 时为空字符串
  seq: number
  senderId: string
  type: number
  content: string
  uuid: string
  createTime: number
  pending: boolean
  failed: boolean
  recalled: boolean
}

const MSG_PAGE_SIZE = 50

function toLocalMsg(m: {
  id: string
  seq: number
  senderId: string
  type: number
  content: string
  uuid: string
  createTime: number
}): LocalMsg {
  return {
    id: m.id,
    seq: m.seq,
    senderId: m.senderId,
    type: m.type,
    content: m.content,
    uuid: m.uuid || `id-${m.id}`,
    createTime: m.createTime,
    pending: false,
    failed: false,
    recalled: false
  }
}

function sortConversations(list: Conversation[]): Conversation[] {
  return [...list].sort((a, b) => {
    const ta = a.lastMsg?.createTime ?? 0
    const tb = b.lastMsg?.createTime ?? 0
    return tb - ta
  })
}

/** 把回执/HTTP 结果落到 pending 消息上 */
function settleMessage(
  state: ChatState,
  conversationId: string,
  uuid: string,
  patch: { id: string; seq: number; createTime: number; failed?: boolean }
): Partial<ChatState> {
  const list = state.messagesByConv[conversationId]
  if (!list) return {}
  return {
    messagesByConv: {
      ...state.messagesByConv,
      [conversationId]: list.map((m) =>
        m.uuid === uuid ? { ...m, ...patch, pending: false } : m
      )
    },
    maxSeqByConv: {
      ...state.maxSeqByConv,
      [conversationId]: Math.max(state.maxSeqByConv[conversationId] ?? 0, patch.seq)
    }
  }
}

interface ChatState {
  conversations: Conversation[]
  convLoaded: boolean
  activeConvId: string | null

  messagesByConv: Record<string, LocalMsg[]>
  maxSeqByConv: Record<string, number>
  membersByConv: Record<string, GroupMember[]>

  friends: Friend[]
  friendRequests: FriendRequest[]

  /** 单聊会话 → 对端 userId（「好友 → 会话」映射） */
  peerByConv: Record<string, string>
  convByPeer: Record<string, string>

  wsStatus: WsStatus

  bootstrap: () => void
  handleWsPayload: (payload: WsMessagePayload) => Promise<void>
  refresh: () => Promise<void>
  refreshSlow: () => Promise<void>
  openConversation: (conversationId: string) => Promise<void>
  closeConversation: () => void
  syncActive: () => Promise<void>
  sendText: (conversationId: string, text: string) => Promise<void>
  recall: (conversationId: string, messageId: string) => Promise<void>

  loadFriends: () => Promise<void>
  loadRequests: () => Promise<void>
  addFriend: (userId: string, applyMsg: string) => Promise<void>
  handleRequest: (requestId: string, agree: boolean) => Promise<void>

  createGroup: (groupName: string, memberIds: string[]) => Promise<void>
  quitGroup: (conversationId: string) => Promise<void>

  reset: () => void
}

let peerResolving = false
let chatBindingsReady = false

export const useChatStore = create<ChatState>((set, get) => ({
  conversations: [],
  convLoaded: false,
  activeConvId: null,
  messagesByConv: {},
  maxSeqByConv: {},
  membersByConv: {},
  friends: [],
  friendRequests: [],
  peerByConv: {},
  convByPeer: {},
  wsStatus: 'idle',

  bootstrap: () => {
    const token = getToken()
    if (!token) return

    if (!chatBindingsReady) {
      wsClient.onStatus((status) => set({ wsStatus: status }))
      wsClient.onMessage((payload) => {
        void get().handleWsPayload(payload)
      })
      wsClient.onKicked((reason) => {
        useAuthStore.getState().logout()
        useChatStore.getState().reset()
        toast(reason || '登录状态已失效，请重新登录', 'error')
      })
      chatBindingsReady = true
    }
    wsClient.connect(token)
  },

  /**
   * WS 下行 "message" 帧处理。
   * 它既可能是自己消息的发送回执（带 clientMsgId），也是新消息的信令；
   * 服务端推送不含正文，统一走「刷新会话列表 + 增量补齐」拿真实内容。
   */
  handleWsPayload: async (payload) => {
    const convId = payload.conversationId
    if (!convId) return

    if (payload.clientMsgId) {
      set((s) =>
        settleMessage(s, convId, payload.clientMsgId, {
          id: payload.messageId,
          seq: payload.seq,
          createTime: payload.createTime
        })
      )
    }

    await get().refresh()
    if (get().activeConvId === convId) {
      await get().syncActive()
    }
  },

  refresh: async () => {
    try {
      const res = await apiConversationList()
      set({ conversations: sortConversations(res.list ?? []), convLoaded: true })
      void resolvePeers()
    } catch (err) {
      handleAuthError(err)
    }
  },

  refreshSlow: async () => {
    await Promise.all([get().loadFriends(), get().loadRequests()]).catch(() => undefined)
  },

  openConversation: async (conversationId) => {
    set({ activeConvId: conversationId })
    const conv = get().conversations.find((c) => c.conversationId === conversationId)

    // 群聊：拉一次成员列表用于昵称展示
    if (conv?.type === 2 && !get().membersByConv[conversationId]) {
      apiGroupMemberList(conversationId)
        .then((res) => {
          set((s) => ({
            membersByConv: { ...s.membersByConv, [conversationId]: res.list ?? [] }
          }))
        })
        .catch(() => undefined)
    }

    if (!get().messagesByConv[conversationId]) {
      try {
        const res = await apiPullMessages(conversationId, MSG_PAGE_SIZE)
        // pull 默认降序（历史翻页），渲染需要升序
        const list = [...(res.list ?? [])].reverse()
        set((s) => ({
          messagesByConv: { ...s.messagesByConv, [conversationId]: list.map(toLocalMsg) },
          maxSeqByConv: {
            ...s.maxSeqByConv,
            [conversationId]: Math.max(res.maxSeq ?? 0, ...list.map((m) => m.seq), 0)
          }
        }))
        markReadIfActive(conversationId)
      } catch (err) {
        handleAuthError(err)
      }
    } else {
      await get().syncActive()
    }
  },

  /** 关闭当前会话（移动端返回会话列表用） */
  closeConversation: () => {
    set({ activeConvId: null })
  },

  /** 增量补齐当前打开会话的新消息 */
  syncActive: async () => {
    const convId = get().activeConvId
    if (!convId) return
    const conv = get().conversations.find((c) => c.conversationId === convId)
    const localMax = get().maxSeqByConv[convId] ?? 0
    if ((conv?.maxSeq ?? 0) <= localMax) return

    try {
      const res = await apiSyncMessages(convId, localMax, MSG_PAGE_SIZE)
      const incoming = res.list ?? []
      set((s) => {
        const existing = s.messagesByConv[convId] ?? []
        if (incoming.length > 0) {
          const seen = new Set(existing.map((m) => m.uuid || `id-${m.id}`))
          const fresh = incoming.filter((m) => !seen.has(m.uuid || `id-${m.id}`)).map(toLocalMsg)
          const merged = [...existing, ...fresh].sort((a, b) => a.seq - b.seq)
          return {
            messagesByConv: { ...s.messagesByConv, [convId]: merged },
            maxSeqByConv: { ...s.maxSeqByConv, [convId]: Math.max(localMax, res.maxSeq ?? 0) }
          }
        }
        return {
          maxSeqByConv: { ...s.maxSeqByConv, [convId]: Math.max(localMax, res.maxSeq ?? 0) }
        }
      })
      markReadIfActive(convId)
    } catch (err) {
      handleAuthError(err)
    }
  },

  sendText: async (conversationId, text) => {
    const trimmed = text.trim()
    if (!trimmed) return
    const meId = useAuthStore.getState().userId
    const uuid = crypto.randomUUID()
    const optimistic: LocalMsg = {
      id: '',
      seq: 0,
      senderId: meId,
      type: 1,
      content: trimmed,
      uuid,
      createTime: Date.now(),
      pending: true,
      failed: false,
      recalled: false
    }

    set((s) => ({
      messagesByConv: {
        ...s.messagesByConv,
        [conversationId]: [...(s.messagesByConv[conversationId] ?? []), optimistic]
      }
    }))

    const sentViaWs = wsClient.send({
      type: 'send',
      conversationId,
      content: trimmed,
      msgType: 1,
      clientMsgId: uuid
    })

    if (sentViaWs) {
      // 正常路径由 WS 回执落库；超时说明回执丢了，HTTP 兜底幂等重发
      setTimeout(() => {
        const stillPending =
          get().messagesByConv[conversationId]?.some((m) => m.uuid === uuid && m.pending) ?? false
        if (stillPending) void confirmSend(conversationId, uuid)
      }, 4000)
      return
    }
    await confirmSend(conversationId, uuid)
  },

  recall: async (conversationId, messageId) => {
    try {
      await apiRecallMessage(conversationId, messageId)
      set((s) => {
        const list = s.messagesByConv[conversationId]
        if (!list) return {}
        return {
          messagesByConv: {
            ...s.messagesByConv,
            [conversationId]: list.map((m) => (m.id === messageId ? { ...m, recalled: true } : m))
          }
        }
      })
      toast('消息已撤回', 'success')
      void get().refresh()
    } catch (err) {
      toast(err instanceof ApiError ? err.message : '撤回失败', 'error')
    }
  },

  loadFriends: async () => {
    try {
      const res = await apiFriendList()
      set({ friends: res.list ?? [] })
    } catch (err) {
      handleAuthError(err)
    }
  },

  loadRequests: async () => {
    try {
      const res = await apiFriendRequestList()
      set({ friendRequests: res.list ?? [] })
    } catch (err) {
      handleAuthError(err)
    }
  },

  addFriend: async (userId, applyMsg) => {
    const res = await apiAddFriend(userId, applyMsg)
    if (res.alreadyFriends) {
      toast('你们已经是好友了', 'info')
    } else {
      toast('好友申请已发送', 'success')
    }
  },

  handleRequest: async (requestId, agree) => {
    await apiHandleFriend(requestId, agree)
    toast(agree ? '已同意好友申请' : '已拒绝好友申请', 'success')
    await Promise.all([get().loadRequests(), get().loadFriends(), get().refresh()])
    await resolvePeers()
  },

  createGroup: async (groupName, memberIds) => {
    const res = await apiCreateGroupChat(groupName, memberIds)
    toast(`群聊创建成功，已邀请 ${res.addedCount} 位成员`, 'success')
    await get().refresh()
    await get().openConversation(res.conversationId)
  },

  quitGroup: async (conversationId) => {
    await apiQuitGroup(conversationId)
    toast('已退出群聊', 'success')
    set((s) => ({
      activeConvId: s.activeConvId === conversationId ? null : s.activeConvId
    }))
    await get().refresh()
  },

  reset: () => {
    wsClient.close()
    set({
      conversations: [],
      convLoaded: false,
      activeConvId: null,
      messagesByConv: {},
      maxSeqByConv: {},
      membersByConv: {},
      friends: [],
      friendRequests: [],
      peerByConv: {},
      convByPeer: {},
      wsStatus: 'idle'
    })
  }
}))

// ── 内部辅助（模块私有） ────────────────────────────────────────────────────

/** HTTP 降级/幂等重发：接口返回后把结果落到 pending 消息上 */
async function confirmSend(conversationId: string, uuid: string): Promise<void> {
  const st = useChatStore.getState()
  const pending = st.messagesByConv[conversationId]?.find((m) => m.uuid === uuid)
  if (!pending) return
  try {
    const res = await apiSendMessage(conversationId, pending.content, uuid)
    useChatStore.setState((s) =>
      settleMessage(s, conversationId, uuid, {
        id: res.id,
        seq: res.seq,
        createTime: res.createTime
      })
    )
    void st.refresh()
  } catch (err) {
    handleAuthError(err)
    useChatStore.setState((s) =>
      settleMessage(s, conversationId, uuid, { id: '', seq: 0, createTime: 0, failed: true })
    )
    toast(err instanceof ApiError ? err.message : '发送失败', 'error')
  }
}

function markReadIfActive(conversationId: string): void {
  const st = useChatStore.getState()
  if (st.activeConvId !== conversationId) return
  const maxSeq = st.maxSeqByConv[conversationId] ?? 0
  if (maxSeq <= 0) return
  apiMarkRead(conversationId, maxSeq)
    .then(() => {
      useChatStore.setState((s) => ({
        conversations: s.conversations.map((c) =>
          c.conversationId === conversationId
            ? { ...c, unreadCount: 0, lastReadSeq: Math.max(c.lastReadSeq, maxSeq) }
            : c
        )
      }))
    })
    .catch(() => undefined)
}

/**
 * 为单聊会话补齐「对端 userId」映射。
 * 会话列表接口不含对端 ID，只能对单聊逐个调 group_user_list（两元素列表）。
 * 结果缓存在 store，每轮 refresh 只补缺失的，不会重复请求。
 */
async function resolvePeers(): Promise<void> {
  if (peerResolving) return
  const st = useChatStore.getState()
  const meId = useAuthStore.getState().userId
  const singles = st.conversations.filter(
    (c) => c.type === 1 && st.peerByConv[c.conversationId] === undefined
  )
  if (singles.length === 0) return

  peerResolving = true
  try {
    const CHUNK = 4
    for (let i = 0; i < singles.length; i += CHUNK) {
      await Promise.all(
        singles.slice(i, i + CHUNK).map(async (conv) => {
          try {
            const res = await apiGroupUserList(conv.conversationId)
            const peer = (res.list ?? []).find((uid) => uid !== meId)
            if (peer) {
              useChatStore.setState((s) => ({
                peerByConv: { ...s.peerByConv, [conv.conversationId]: peer },
                convByPeer: { ...s.convByPeer, [peer]: conv.conversationId }
              }))
            }
          } catch {
            /* 单条失败跳过，下轮 refresh 再补 */
          }
        })
      )
    }
  } finally {
    peerResolving = false
  }
}

function handleAuthError(err: unknown): void {
  if (err instanceof ApiError && (err.code === CODE_UNAUTHORIZED || err.code === CODE_TOKEN_REVOKED)) {
    useAuthStore.getState().logout()
    useChatStore.getState().reset()
    toast('登录已过期，请重新登录', 'error')
  }
}
