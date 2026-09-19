import { useEffect, useMemo, useRef, useState } from 'react'
import { AnimatePresence, motion } from 'framer-motion'
import { useChatStore, type LocalMsg } from '@/store/chat'
import { useAuthStore } from '@/store/auth'
import { Avatar } from '@/components/Avatar'
import { EmptyState } from '@/components/EmptyState'
import { ChatIcon, SearchIcon, SendIcon, GroupIcon, ClockIcon, AlertIcon } from '@/components/icons'
import { formatDayLabel, formatListTime, formatMsgTime } from '@/core/format'
import type { Conversation } from '@/api/types'

/**
 * 消息页：左侧会话卡片流 + 右侧聊天面板。
 * 卡片悬浮于底色之上（非简单切块），切换会话时面板内容带滑入过渡。
 */
export function ChatView({ onGoContacts }: { onGoContacts: () => void }): JSX.Element {
  const conversations = useChatStore((s) => s.conversations)
  const convLoaded = useChatStore((s) => s.convLoaded)
  const activeConvId = useChatStore((s) => s.activeConvId)
  const openConversation = useChatStore((s) => s.openConversation)

  const [keyword, setKeyword] = useState('')

  const filtered = useMemo(() => {
    const kw = keyword.trim().toLowerCase()
    if (!kw) return conversations
    return conversations.filter((c) =>
      (c.aliasName || c.name || '').toLowerCase().includes(kw)
    )
  }, [conversations, keyword])

  const activeConv = conversations.find((c) => c.conversationId === activeConvId)
  const closeConversation = useChatStore((s) => s.closeConversation)

  return (
    <div className={`chat-layout ${activeConv ? 'has-active' : ''}`}>
      <motion.section
        className="panel conv-panel"
        initial={{ opacity: 0, x: -16 }}
        animate={{ opacity: 1, x: 0 }}
        transition={{ type: 'spring', stiffness: 280, damping: 30 }}
      >
        <div className="panel-glass-header">
          <div className="search-box">
            <SearchIcon width={14} height={14} />
            <input
              placeholder="搜索会话"
              value={keyword}
              onChange={(e) => setKeyword(e.target.value)}
            />
          </div>
        </div>

        <div className="conv-scroll">
          {convLoaded && filtered.length === 0 ? (
            <EmptyState
              icon={<ChatIcon width={34} height={34} />}
              title="还没有会话"
              hint="添加好友或创建群聊，开始你的第一次对话"
            >
              <motion.button
                className="primary-btn small"
                whileHover={{ y: -1 }}
                whileTap={{ scale: 0.96 }}
                onClick={onGoContacts}
              >
                去添加好友
              </motion.button>
            </EmptyState>
          ) : (
            <AnimatePresence initial={false}>
              {filtered.map((conv) => (
                <ConversationCard
                  key={conv.conversationId}
                  conv={conv}
                  active={conv.conversationId === activeConvId}
                  onClick={() => void openConversation(conv.conversationId)}
                />
              ))}
            </AnimatePresence>
          )}
        </div>
      </motion.section>

      <AnimatePresence mode="wait">
        {activeConv ? (
          <motion.section
            key={activeConv.conversationId}
            className="panel chat-panel"
            initial={{ opacity: 0, x: 22, scale: 0.995 }}
            animate={{ opacity: 1, x: 0, scale: 1 }}
            exit={{ opacity: 0, x: -14, scale: 0.997 }}
            transition={{ type: 'spring', stiffness: 300, damping: 30 }}
          >
            <ChatPanel conv={activeConv} onBack={closeConversation} />
          </motion.section>
        ) : (
          <motion.section
            key="empty-chat"
            className="panel chat-panel chat-panel-empty"
            initial={{ opacity: 0, scale: 0.995 }}
            animate={{ opacity: 1, scale: 1 }}
            exit={{ opacity: 0 }}
            transition={{ duration: 0.2 }}
          >
            <EmptyState
              icon={<ChatIcon width={40} height={40} />}
              title="选择一个会话"
              hint="从左侧挑选一段对话，或开启新的相遇"
            />
          </motion.section>
        )}
      </AnimatePresence>
    </div>
  )
}

/** 会话卡片：头像 + 名称 + 摘要 + 未读徽标，激活时左侧光条指示 */
function ConversationCard({
  conv,
  active,
  onClick
}: {
  conv: Conversation
  active: boolean
  onClick: () => void
}): JSX.Element {
  const meId = useAuthStore((s) => s.userId)
  const name = conv.aliasName || conv.name || `会话 ${conv.conversationId}`
  const draft = useChatStore((s) => s.messagesByConv[conv.conversationId]?.some((m) => m.failed))

  const preview = conv.lastMsg
    ? conv.lastMsg.content.length > 40
      ? `${conv.lastMsg.content.slice(0, 40)}…`
      : conv.lastMsg.content
    : '暂无消息'

  return (
    <motion.button
      className={`conv-card ${active ? 'active' : ''}`}
      onClick={onClick}
      initial={{ opacity: 0, y: 8 }}
      animate={{ opacity: 1, y: 0 }}
      exit={{ opacity: 0, x: -20 }}
      whileHover={{ x: 2 }}
      whileTap={{ scale: 0.985 }}
      transition={{ type: 'spring', stiffness: 420, damping: 34 }}
    >
      <span className="conv-card-avatar">
        <Avatar
          name={name}
          seed={String(conv.conversationId)}
          url={conv.avatarUrl}
          size={44}
          square={conv.type !== 2}
        />
        {conv.type === 2 && <span className="conv-group-tag" title="群聊"><GroupIcon width={9} height={9} /></span>}
      </span>
      <span className="conv-card-main">
        <span className="conv-card-row">
          <span className="conv-card-name">{name}</span>
          <span className="conv-card-time">
            {formatListTime(conv.lastMsg?.createTime ?? 0)}
          </span>
        </span>
        <span className="conv-card-row">
          <span className={`conv-card-preview ${draft ? 'has-failed' : ''}`}>
            {conv.lastMsg?.senderId === meId ? '我: ' : ''}
            {preview}
          </span>
          {conv.unreadCount > 0 ? (
            <motion.span
              key={conv.unreadCount}
              className="conv-unread"
              initial={{ scale: 0.5 }}
              animate={{ scale: [0.5, 1.2, 1] }}
              transition={{ duration: 0.3 }}
            >
              {conv.unreadCount > 99 ? '99+' : conv.unreadCount}
            </motion.span>
          ) : null}
        </span>
      </span>
      {active && <motion.span layoutId="conv-active-bar" className="conv-active-bar" />}
    </motion.button>
  )
}

/** 聊天面板：头部（会话信息）+ 消息流 + 输入区 */
function ChatPanel({ conv, onBack }: { conv: Conversation; onBack: () => void }): JSX.Element {
  const name = conv.aliasName || conv.name || `会话 ${conv.conversationId}`
  const messages = useChatStore((s) => s.messagesByConv[conv.conversationId]) ?? []
  const members = useChatStore((s) => s.membersByConv[conv.conversationId])
  const sendText = useChatStore((s) => s.sendText)
  const recall = useChatStore((s) => s.recall)
  const wsStatus = useChatStore((s) => s.wsStatus)
  const profile = useAuthStore((s) => s.profile)
  const meId = useAuthStore((s) => s.userId)

  const [draft, setDraft] = useState('')
  const [menu, setMenu] = useState<{ x: number; y: number; msg: LocalMsg } | null>(null)
  const scrollRef = useRef<HTMLDivElement>(null)
  const stickBottom = useRef(true)
  const textareaRef = useRef<HTMLTextAreaElement>(null)

  // 新消息时保持在底部附近则吸底
  useEffect(() => {
    const el = scrollRef.current
    if (!el) return
    if (stickBottom.current) {
      el.scrollTop = el.scrollHeight
    }
  }, [messages])

  const onScroll = (): void => {
    const el = scrollRef.current
    if (!el) return
    stickBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < 60
  }

  const send = (): void => {
    const text = draft.trim()
    if (!text) return
    setDraft('')
    stickBottom.current = true
    void sendText(conv.conversationId, text)
  }

  const memberName = (senderId: number): string => {
    if (senderId === meId) return profile?.nickName ?? '我'
    const m = members?.find((x) => x.userId === senderId)
    return m?.nickName || m?.aliasName || `用户 ${senderId}`
  }

  const subtitle =
    conv.type === 2
      ? `群聊 · ${members?.length > 0 ? `${members.length} 人` : '加载成员中…'}`
      : '单聊'

  return (
    <div className="chat-inner">
      <header className="chat-header">
        <button className="chat-back-btn" title="返回会话列表" onClick={onBack}>
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round">
            <path d="M15 18l-6-6 6-6" />
          </svg>
        </button>
        <div className="chat-header-info">
          <Avatar
            name={name}
            seed={String(conv.conversationId)}
            url={conv.avatarUrl}
            size={34}
            square={conv.type !== 2}
          />
          <div>
            <div className="chat-header-name">{name}</div>
            <div className="chat-header-sub">{subtitle}</div>
          </div>
        </div>
        {conv.type === 2 && <span className="chat-header-badge">GROUP</span>}
      </header>

      <div className="msg-scroll" ref={scrollRef} onScroll={onScroll}>
        {messages.length === 0 ? (
          <EmptyState
            icon={<ChatIcon width={30} height={30} />}
            title="还没有消息"
            hint="说点什么，开启这段对话"
          />
        ) : (
          messages.map((msg, idx) => {
            const prev = messages[idx - 1]
            const showDay =
              !prev || new Date(prev.createTime).toDateString() !== new Date(msg.createTime).toDateString()
            return (
              <div key={msg.uuid || `id-${msg.id}`}>
                {showDay && (
                  <div className="msg-day">
                    <span>{formatDayLabel(msg.createTime)}</span>
                  </div>
                )}
                <MessageRow
                  msg={msg}
                  mine={msg.senderId === meId}
                  showSender={conv.type === 2 && msg.senderId !== meId}
                  senderName={memberName(msg.senderId)}
                  avatarUrl={
                    msg.senderId === meId
                      ? profile?.avatarUrl
                      : members?.find((x) => x.userId === msg.senderId)?.avatarUrl
                  }
                  recallable={msg.senderId === meId && !msg.pending && !msg.failed && msg.id > 0}
                  onContextMenu={(x, y) => setMenu({ x, y, msg })}
                />
              </div>
            )
          })
        )}
      </div>

      <footer className="composer">
        <textarea
          ref={textareaRef}
          value={draft}
          placeholder="输入消息… (Enter 发送 / Shift+Enter 换行)"
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
              e.preventDefault()
              send()
            }
          }}
        />
        <div className="composer-bar">
          <span className={`ws-hint ${wsStatus === 'open' ? 'ok' : 'off'}`}>
            {wsStatus === 'open' ? '实时通道已连接' : '离线模式 · 将通过 HTTP 发送'}
          </span>
          <motion.button
            className={`send-btn ${draft.trim() ? 'ready' : ''}`}
            whileHover={draft.trim() ? { y: -1, scale: 1.02 } : {}}
            whileTap={{ scale: 0.94 }}
            onClick={send}
            disabled={!draft.trim()}
          >
            <SendIcon width={15} height={15} />
            发送
          </motion.button>
        </div>
      </footer>

      <AnimatePresence>
        {menu && (
          <>
            <div className="ctx-backdrop" onClick={() => setMenu(null)} onContextMenu={(e) => { e.preventDefault(); setMenu(null) }} />
            <motion.div
              className="ctx-menu"
              style={{ left: menu.x, top: menu.y }}
              initial={{ opacity: 0, scale: 0.85 }}
              animate={{ opacity: 1, scale: 1 }}
              exit={{ opacity: 0, scale: 0.9 }}
              transition={{ type: 'spring', stiffness: 480, damping: 30 }}
            >
              <button
                onClick={() => {
                  void recall(conv.conversationId, menu.msg.id)
                  setMenu(null)
                }}
              >
                撤回消息
              </button>
            </motion.div>
          </>
        )}
      </AnimatePresence>
    </div>
  )
}

/** 单条消息：自己的消息蓝底白字靠右，对方白底靠左 */
function MessageRow({
  msg,
  mine,
  showSender,
  senderName,
  avatarUrl,
  recallable,
  onContextMenu
}: {
  msg: LocalMsg
  mine: boolean
  showSender: boolean
  senderName: string
  avatarUrl?: string
  recallable: boolean
  onContextMenu: (x: number, y: number) => void
}): JSX.Element {
  const [hover, setHover] = useState(false)

  if (msg.recalled) {
    return (
      <div className="msg-row recalled">
        <span className="msg-notice">
          <ClockIcon width={11} height={11} />
          {mine ? '你撤回了一条消息' : `${senderName} 撤回了一条消息`}
        </span>
      </div>
    )
  }

  return (
    <motion.div
      className={`msg-row ${mine ? 'mine' : 'theirs'}`}
      initial={{ opacity: 0, y: 12, scale: 0.96 }}
      animate={{ opacity: 1, y: 0, scale: 1 }}
      transition={{ type: 'spring', stiffness: 380, damping: 30 }}
      onMouseEnter={() => setHover(true)}
      onMouseLeave={() => setHover(false)}
      onContextMenu={(e) => {
        if (!recallable) return
        e.preventDefault()
        onContextMenu(e.clientX, e.clientY)
      }}
    >
      <Avatar name={senderName} seed={String(msg.senderId)} url={avatarUrl} size={36} square={false} />
      <div className={`msg-body ${mine ? 'mine' : 'theirs'}`}>
        {showSender && <div className="msg-sender">{senderName}</div>}
        <div className={`bubble ${mine ? 'mine' : 'theirs'} ${msg.pending ? 'pending' : ''} ${msg.failed ? 'failed' : ''}`}>
          <span className="bubble-text">{msg.content}</span>
          {msg.pending && <span className="bubble-status"><ClockIcon width={11} height={11} /></span>}
          {msg.failed && (
            <span className="bubble-status failed-status" title="发送失败">
              <AlertIcon width={11} height={11} />
            </span>
          )}
        </div>
        <div className="msg-meta">{formatMsgTime(msg.createTime)}</div>
      </div>
      {hover && recallable && <span className="msg-recall-hint">右键撤回</span>}
    </motion.div>
  )
}
