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
import { confirmMessage, failMessage, mergeMessages, prepareRetry, receivedCursor, type LocalMsg } from '@/core/message-state'
import type { Conversation, Friend, FriendRequest, GroupMember, WsMessagePayload } from '@/api/types'
import { useAuthStore } from './auth'
import { toast } from './toast'

export type { LocalMsg } from '@/core/message-state'

const MSG_PAGE_SIZE = 50

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
  patch: { id: string; seq: number; createTime: number }
): Partial<ChatState> {
  const list = state.messagesByConv[conversationId]
  if (!list) return {}
  // 下行广播也携带发送方的 clientMsgId。只有本地确实存在该消息时，
  // 才能把它当发送回执。即使确认发送成功，也不能跳过尚未补齐的其他正文。
  if (!list.some((m) => m.uuid === uuid)) return {}
  return {
    messagesByConv: {
      ...state.messagesByConv,
      [conversationId]: confirmMessage(list, uuid, patch)
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
  historyByConv: Record<string, { hasMore: boolean; loading: boolean; error: string }>

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
  loadOlder: (conversationId: string) => Promise<void>
  sendText: (conversationId: string, text: string) => Promise<void>
  retryText: (conversationId: string, uuid: string) => Promise<void>
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
const syncingConversations = new Set<string>()

export const useChatStore = create<ChatState>((set, get) => ({
  conversations: [],
  convLoaded: false,
  activeConvId: null,
  messagesByConv: {},
  maxSeqByConv: {},
  membersByConv: {},
  historyByConv: {},
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
        const auth = useAuthStore.getState()
        if (auth.status !== 'authed') return
        auth.invalidateSession()
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
    const convId = get().activeConvId
    if (convId && get().conversations.some((c) => c.conversationId === convId && c.type === 2)) {
      try {
        const res = await apiGroupMemberList(convId)
        set((s) => ({ membersByConv: { ...s.membersByConv, [convId]: res.list ?? [] } }))
      } catch (err) {
        handleAuthError(err)
      }
    }
  },

  openConversation: async (conversationId) => {
    set({ activeConvId: conversationId })
    const conv = get().conversations.find((c) => c.conversationId === conversationId)

    // 重新进入群聊时刷新成员资料，避免沿用旧昵称、头像或已退群成员。
    if (conv?.type === 2) {
      apiGroupMemberList(conversationId)
        .then((res) => {
          set((s) => ({
            membersByConv: { ...s.membersByConv, [conversationId]: res.list ?? [] }
          }))
        })
        .catch(() => undefined)
    }

    if (!get().messagesByConv[conversationId]) {
      const token = getToken()
      set((s) => ({ historyByConv: { ...s.historyByConv, [conversationId]: { hasMore: false, loading: true, error: '' } } }))
      try {
        const res = await apiPullMessages(conversationId, MSG_PAGE_SIZE)
        if (getToken() !== token) return
        const list = res.list ?? []
        set((s) => ({
          messagesByConv: { ...s.messagesByConv, [conversationId]: mergeMessages(s.messagesByConv[conversationId] ?? [], list) },
          historyByConv: { ...s.historyByConv, [conversationId]: { hasMore: res.hasMore, loading: false, error: '' } },
          maxSeqByConv: {
            ...s.maxSeqByConv,
            [conversationId]: Math.max(res.maxSeq ?? 0, ...list.map((m) => m.seq), 0)
          }
        }))
        markReadIfActive(conversationId)
      } catch (err) {
        if (getToken() !== token) return
        set((s) => ({ historyByConv: { ...s.historyByConv, [conversationId]: { hasMore: false, loading: false, error: '消息加载失败，请重试' } } }))
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
    if (!convId || !get().messagesByConv[convId] || syncingConversations.has(convId)) return
    const conv = get().conversations.find((c) => c.conversationId === convId)
    const localMax = get().maxSeqByConv[convId] ?? 0
    if ((conv?.maxSeq ?? 0) <= localMax) return

    const token = getToken()
    syncingConversations.add(convId)
    try {
      let cursor = localMax
      do {
        const res = await apiSyncMessages(convId, cursor, MSG_PAGE_SIZE)
        if (getToken() !== token || get().activeConvId !== convId) return
        const incoming = res.list ?? []
        // 兼容旧服务的 maxSeq：有消息时只推进到真正拿到的正文，不能跳页。
        const next = incoming.length ? receivedCursor(cursor, incoming) : Math.max(cursor, res.maxSeq ?? 0)
        set((s) => ({
          messagesByConv: { ...s.messagesByConv, [convId]: mergeMessages(s.messagesByConv[convId] ?? [], incoming) },
          maxSeqByConv: { ...s.maxSeqByConv, [convId]: Math.max(s.maxSeqByConv[convId] ?? 0, next) }
        }))
        if (!res.hasMore || next <= cursor) break
        cursor = next
      } while (get().activeConvId === convId)
      markReadIfActive(convId)
    } catch (err) {
      handleAuthError(err)
    } finally {
      syncingConversations.delete(convId)
    }
  },

  loadOlder: async (conversationId) => {
    const history = get().historyByConv[conversationId]
    if (!history?.hasMore || history.loading) return
    const seqs = (get().messagesByConv[conversationId] ?? []).filter((m) => m.seq > 0).map((m) => m.seq)
    const oldest = Math.min(...seqs)
    if (oldest <= 1 || !Number.isFinite(oldest)) {
      set((s) => ({ historyByConv: { ...s.historyByConv, [conversationId]: { ...history, hasMore: false } } }))
      return
    }
    const token = getToken()
    set((s) => ({ historyByConv: { ...s.historyByConv, [conversationId]: { ...history, loading: true, error: '' } } }))
    try {
      const res = await apiPullMessages(conversationId, MSG_PAGE_SIZE, oldest - 1)
      if (getToken() !== token) return
      set((s) => ({
        messagesByConv: { ...s.messagesByConv, [conversationId]: mergeMessages(s.messagesByConv[conversationId] ?? [], res.list ?? []) },
        historyByConv: { ...s.historyByConv, [conversationId]: { hasMore: res.hasMore, loading: false, error: '' } }
      }))
    } catch (err) {
      if (getToken() !== token) return
      set((s) => ({ historyByConv: { ...s.historyByConv, [conversationId]: { ...history, loading: false, error: '历史消息加载失败，点击重试' } } }))
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

  retryText: async (conversationId, uuid) => {
    const list = get().messagesByConv[conversationId] ?? []
    if (!list.some((m) => m.uuid === uuid && m.failed && !m.pending && m.id === '')) return
    set((s) => ({ messagesByConv: {
      ...s.messagesByConv,
      [conversationId]: prepareRetry(s.messagesByConv[conversationId] ?? [], uuid)
    } }))
    // 原 uuid 即服务端幂等键；不追加新气泡，重复点击也不会产生并发重试。
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
      historyByConv: {},
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
  if (!pending?.pending) return
  const token = getToken()
  try {
    const res = await apiSendMessage(conversationId, pending.content, uuid)
    if (getToken() !== token) return
    useChatStore.setState((s) =>
      settleMessage(s, conversationId, uuid, {
        id: res.id,
        seq: res.seq,
        createTime: res.createTime
      })
    )
    void st.refresh()
  } catch (err) {
    if (getToken() !== token) return
    const unconfirmed = useChatStore.getState().messagesByConv[conversationId]
      ?.some((m) => m.uuid === uuid && m.pending && m.id === '')
    if (!unconfirmed) return
    useChatStore.setState((s) => ({ messagesByConv: {
      ...s.messagesByConv,
      [conversationId]: failMessage(s.messagesByConv[conversationId] ?? [], uuid)
    } }))
    handleAuthError(err)
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
    const auth = useAuthStore.getState()
    if (auth.status !== 'authed') return
    auth.invalidateSession()
    useChatStore.getState().reset()
    toast('登录已过期，请重新登录', 'error')
  }
}
