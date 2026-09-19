import { useEffect, useMemo, useState } from 'react'
import { AnimatePresence, motion } from 'framer-motion'
import { useChatStore } from '@/store/chat'
import { useAuthStore } from '@/store/auth'
import { ApiError } from '@/api/client'
import { toast } from '@/store/toast'
import { Avatar } from '@/components/Avatar'
import { EmptyState } from '@/components/EmptyState'
import { Modal } from '@/components/Modal'
import {
  ContactsIcon,
  SearchIcon,
  AddFriendIcon,
  GroupIcon,
  BellIcon,
  ChatIcon,
  CheckIcon,
  CloseIcon
} from '@/components/icons'
import { formatListTime } from '@/core/format'
import type { NavKey } from './Shell'

/**
 * 联系人页：好友列表 / 好友申请 两个子页签 + 顶部操作（加好友、建群）。
 * 子页签切换带方向感滑动。
 */
export function ContactsView({ onNav }: { onNav: (key: NavKey) => void }): JSX.Element {
  const [tab, setTab] = useState<'friends' | 'requests'>('friends')
  const [keyword, setKeyword] = useState('')
  const [addOpen, setAddOpen] = useState(false)
  const [groupOpen, setGroupOpen] = useState(false)

  const friends = useChatStore((s) => s.friends)
  const requests = useChatStore((s) => s.friendRequests)
  const loadFriends = useChatStore((s) => s.loadFriends)
  const loadRequests = useChatStore((s) => s.loadRequests)
  const openConversation = useChatStore((s) => s.openConversation)
  const convByPeer = useChatStore((s) => s.convByPeer)

  const pending = useMemo(() => requests.filter((r) => r.status === 0), [requests])

  useEffect(() => {
    void loadFriends()
    void loadRequests()
  }, [loadFriends, loadRequests])

  const filtered = useMemo(() => {
    const kw = keyword.trim().toLowerCase()
    if (!kw) return friends
    return friends.filter(
      (f) =>
        f.nickName.toLowerCase().includes(kw) ||
        f.remark.toLowerCase().includes(kw) ||
        String(f.userId).includes(kw)
    )
  }, [friends, keyword])

  const openChatWith = (userId: string): void => {
    const convId = convByPeer[userId]
    if (!convId) {
      toast('会话暂未同步，稍等片刻再试', 'info')
      return
    }
    onNav('chat')
    void openConversation(convId)
  }

  return (
    <motion.div
      className="contacts-layout"
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      transition={{ duration: 0.2 }}
    >
      <section className="panel contacts-panel">
        <div className="panel-glass-header contacts-header">
          <div className="seg-tabs">
            {(
              [
                { key: 'friends', label: `好友 ${friends.length || ''}` },
                { key: 'requests', label: `申请 ${pending.length || ''}` }
              ] as const
            ).map((t) => (
              <button key={t.key} className="seg-tab" onClick={() => setTab(t.key)}>
                {tab === t.key && (
                  <motion.span
                    layoutId="contacts-seg-pill"
                    className="seg-pill"
                    transition={{ type: 'spring', stiffness: 420, damping: 34 }}
                  />
                )}
                <span className={tab === t.key ? 'seg-text active' : 'seg-text'}>{t.label}</span>
              </button>
            ))}
          </div>
          <div className="contacts-actions">
            <motion.button
              className="icon-chip"
              title="添加好友"
              whileHover={{ y: -1, rotate: -4 }}
              whileTap={{ scale: 0.92 }}
              onClick={() => setAddOpen(true)}
            >
              <AddFriendIcon width={16} height={16} />
            </motion.button>
            <motion.button
              className="icon-chip"
              title="创建群聊"
              whileHover={{ y: -1, rotate: 4 }}
              whileTap={{ scale: 0.92 }}
              onClick={() => setGroupOpen(true)}
            >
              <GroupIcon width={16} height={16} />
            </motion.button>
          </div>
        </div>

        {tab === 'friends' && (
          <div className="search-box list-search">
            <SearchIcon width={14} height={14} />
            <input
              placeholder="搜索好友 / ID"
              value={keyword}
              onChange={(e) => setKeyword(e.target.value)}
            />
          </div>
        )}

        <div className="contacts-scroll">
          <AnimatePresence mode="wait">
            {tab === 'friends' ? (
              <motion.div
                key="friends"
                initial={{ opacity: 0, x: -18 }}
                animate={{ opacity: 1, x: 0 }}
                exit={{ opacity: 0, x: -18 }}
                transition={{ duration: 0.18 }}
              >
                {filtered.length === 0 ? (
                  <EmptyState
                    icon={<ContactsIcon width={34} height={34} />}
                    title={keyword ? '没有匹配的好友' : '还没有好友'}
                    hint={keyword ? '' : '通过右上角按钮添加好友'}
                  />
                ) : (
                  filtered.map((f, i) => (
                    <motion.button
                      key={f.userId}
                      className="contact-card"
                      initial={{ opacity: 0, y: 10 }}
                      animate={{ opacity: 1, y: 0 }}
                      exit={{ opacity: 0 }}
                      transition={{ delay: Math.min(i * 0.02, 0.3), duration: 0.22 }}
                      whileHover={{ x: 2 }}
                      whileTap={{ scale: 0.99 }}
                      onClick={() => openChatWith(f.userId)}
                    >
                      <Avatar name={f.remark || f.nickName} url={f.avatarUrl} seed={String(f.userId)} size={42} />
                      <span className="contact-main">
                        <span className="contact-name">{f.remark || f.nickName}</span>
                        <span className="contact-sub">ID: {f.userId}</span>
                      </span>
                      <motion.span className="contact-chat-hint" whileHover={{ scale: 1.08 }}>
                        <ChatIcon width={15} height={15} />
                      </motion.span>
                    </motion.button>
                  ))
                )}
              </motion.div>
            ) : (
              <motion.div
                key="requests"
                initial={{ opacity: 0, x: 18 }}
                animate={{ opacity: 1, x: 0 }}
                exit={{ opacity: 0, x: 18 }}
                transition={{ duration: 0.18 }}
              >
                {requests.length === 0 ? (
                  <EmptyState icon={<BellIcon width={32} height={32} />} title="暂无好友申请" />
                ) : (
                  requests.map((r, i) => (
                    <RequestCard key={r.id} req={r} index={i} />
                  ))
                )}
              </motion.div>
            )}
          </AnimatePresence>
        </div>
      </section>

      <AddFriendModal open={addOpen} onClose={() => setAddOpen(false)} />
      <CreateGroupModal open={groupOpen} onClose={() => setGroupOpen(false)} />
    </motion.div>
  )
}

/** 好友申请卡片：待处理带 同意/拒绝，已处理显示状态 */
function RequestCard({
  req,
  index
}: {
  req: {
    id: string
    fromUid: string
    toUid: string
    applyMsg: string
    status: number
    createdAt: number
  }
  index: number
}): JSX.Element {
  const meId = useAuthStore((s) => s.userId)
  const handleRequest = useChatStore((s) => s.handleRequest)
  const [busy, setBusy] = useState(false)

  const incoming = req.fromUid !== meId
  const peerId = incoming ? req.fromUid : req.toUid
  const statusText =
    req.status === 1 ? '已同意' : req.status === 2 ? '已拒绝' : req.status === 3 ? '已过期' : ''

  const act = async (agree: boolean): Promise<void> => {
    if (busy) return
    setBusy(true)
    try {
      await handleRequest(req.id, agree)
    } catch (err) {
      toast(err instanceof ApiError ? err.message : '操作失败', 'error')
    } finally {
      setBusy(false)
    }
  }

  return (
    <motion.div
      className={`request-card ${req.status === 0 ? 'pending' : 'done'}`}
      initial={{ opacity: 0, y: 10 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ delay: Math.min(index * 0.03, 0.3), duration: 0.22 }}
    >
      <Avatar name={`用户 ${peerId}`} seed={String(peerId)} size={42} />
      <div className="request-main">
        <div className="request-title">
          {incoming ? `用户 ${peerId}` : `我 → 用户 ${peerId}`}
          <span className="request-dir">{incoming ? '请求添加你为好友' : '等待对方处理'}</span>
        </div>
        <div className="request-msg">{req.applyMsg || '我是…'}</div>
        <div className="request-time">{formatListTime(req.createdAt)}</div>
      </div>
      {req.status === 0 ? (
        incoming ? (
          <div className="request-actions">
            <motion.button
              className="mini-btn primary"
              whileTap={{ scale: 0.92 }}
              disabled={busy}
              onClick={() => void act(true)}
            >
              <CheckIcon width={12} height={12} />
              同意
            </motion.button>
            <motion.button
              className="mini-btn ghost"
              whileTap={{ scale: 0.92 }}
              disabled={busy}
              onClick={() => void act(false)}
            >
              <CloseIcon width={12} height={12} />
              拒绝
            </motion.button>
          </div>
        ) : (
          <span className="request-waiting">待对方确认</span>
        )
      ) : (
        <span className={`request-status s${req.status}`}>{statusText}</span>
      )}
    </motion.div>
  )
}

/** 添加好友弹窗 */
function AddFriendModal({ open, onClose }: { open: boolean; onClose: () => void }): JSX.Element {
  const [userId, setUserId] = useState('')
  const [applyMsg, setApplyMsg] = useState('')
  const [busy, setBusy] = useState(false)
  const addFriend = useChatStore((s) => s.addFriend)

  const submit = async (): Promise<void> => {
    const id = userId.trim()
    if (!/^\d+$/.test(id) || id === '0' || id.length > 19) {
      toast('请输入正确的用户 ID', 'error')
      return
    }
    if (busy) return
    setBusy(true)
    try {
      await addFriend(id, applyMsg.trim())
      onClose()
      setUserId('')
      setApplyMsg('')
    } catch (err) {
      toast(err instanceof ApiError ? err.message : '发送失败', 'error')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal open={open} title="添加好友" onClose={onClose}>
      <label className="stack-field">
        <span>对方用户 ID</span>
        <input
          inputMode="numeric"
          placeholder="例如 1024"
          value={userId}
          onChange={(e) => setUserId(e.target.value.replace(/\D/g, ''))}
          onKeyDown={(e) => e.key === 'Enter' && void submit()}
          autoFocus
        />
      </label>
      <label className="stack-field">
        <span>验证消息</span>
        <input
          placeholder="你好，我是…"
          value={applyMsg}
          maxLength={60}
          onChange={(e) => setApplyMsg(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && void submit()}
        />
      </label>
      <div className="modal-actions">
        <button className="ghost-btn" onClick={onClose}>
          取消
        </button>
        <button className="primary-btn" disabled={busy} onClick={() => void submit()}>
          {busy ? '发送中…' : '发送申请'}
        </button>
      </div>
    </Modal>
  )
}

/** 创建群聊弹窗：从好友中多选初始成员 */
function CreateGroupModal({ open, onClose }: { open: boolean; onClose: () => void }): JSX.Element {
  const friends = useChatStore((s) => s.friends)
  const createGroup = useChatStore((s) => s.createGroup)
  const [groupName, setGroupName] = useState('')
  const [picked, setPicked] = useState<string[]>([])
  const [busy, setBusy] = useState(false)

  const toggle = (userId: string): void => {
    setPicked((prev) =>
      prev.includes(userId) ? prev.filter((x) => x !== userId) : [...prev, userId]
    )
  }

  const submit = async (): Promise<void> => {
    const name = groupName.trim()
    if (!name) {
      toast('请填写群名称', 'error')
      return
    }
    if (busy) return
    setBusy(true)
    try {
      await createGroup(name, picked)
      onClose()
      setGroupName('')
      setPicked([])
    } catch (err) {
      toast(err instanceof ApiError ? err.message : '创建失败', 'error')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal open={open} title="创建群聊" width={460} onClose={onClose}>
      <label className="stack-field">
        <span>群名称</span>
        <input
          placeholder="给群聊起个名字"
          value={groupName}
          maxLength={30}
          onChange={(e) => setGroupName(e.target.value)}
          autoFocus
        />
      </label>
      <div className="stack-field">
        <span>选择成员（已选 {picked.length}）</span>
        <div className="member-picker">
          {friends.length === 0 ? (
            <p className="picker-empty">先去添加一些好友吧</p>
          ) : (
            friends.map((f) => (
              <motion.button
                key={f.userId}
                className={`member-chip ${picked.includes(f.userId) ? 'picked' : ''}`}
                whileTap={{ scale: 0.94 }}
                onClick={() => toggle(f.userId)}
              >
                <Avatar name={f.remark || f.nickName} url={f.avatarUrl} seed={String(f.userId)} size={26} />
                <span>{f.remark || f.nickName}</span>
                {picked.includes(f.userId) && (
                  <motion.span
                    className="chip-check"
                    initial={{ scale: 0 }}
                    animate={{ scale: 1 }}
                    transition={{ type: 'spring', stiffness: 500, damping: 24 }}
                  >
                    <CheckIcon width={10} height={10} />
                  </motion.span>
                )}
              </motion.button>
            ))
          )}
        </div>
      </div>
      <div className="modal-actions">
        <button className="ghost-btn" onClick={onClose}>
          取消
        </button>
        <button className="primary-btn" disabled={busy} onClick={() => void submit()}>
          {busy ? '创建中…' : '创建群聊'}
        </button>
      </div>
    </Modal>
  )
}
