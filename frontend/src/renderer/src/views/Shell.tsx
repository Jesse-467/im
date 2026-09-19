import { useEffect, useRef, useState } from 'react'
import { AnimatePresence, motion } from 'framer-motion'
import { useAuthStore } from '@/store/auth'
import { useChatStore } from '@/store/chat'
import { toast } from '@/store/toast'
import { TitleBar, ConnectionDot } from '@/components/TitleBar'
import { Avatar } from '@/components/Avatar'
import { ChatIcon, ContactsIcon, SettingsIcon, LogoutIcon } from '@/components/icons'
import { ChatView } from './ChatView'
import { ContactsView } from './ContactsView'
import { MeView } from './MeView'

export type NavKey = 'chat' | 'contacts' | 'me'

const NAV_ITEMS: { key: NavKey; label: string; icon: typeof ChatIcon }[] = [
  { key: 'chat', label: '消息', icon: ChatIcon },
  { key: 'contacts', label: '联系人', icon: ContactsIcon },
  { key: 'me', label: '设置', icon: SettingsIcon }
]

const NAV_ORDER: NavKey[] = ['chat', 'contacts', 'me']

/**
 * 主框架：标题栏 + 左侧功能条 + 视图区。
 * 视图切换带方向感的滑入滑出；消息/申请红点驱动导航徽标。
 */
export function Shell(): JSX.Element {
  const [nav, setNav] = useState<NavKey>('chat')
  const [confirmLogout, setConfirmLogout] = useState(false)
  const prevNav = useRef<NavKey>('chat')

  const profile = useAuthStore((s) => s.profile)
  const logout = useAuthStore((s) => s.logout)
  const reset = useChatStore((s) => s.reset)

  const conversations = useChatStore((s) => s.conversations)
  const friendRequests = useChatStore((s) => s.friendRequests)
  const wsStatus = useChatStore((s) => s.wsStatus)
  const bootstrap = useChatStore((s) => s.bootstrap)
  const refresh = useChatStore((s) => s.refresh)
  const refreshSlow = useChatStore((s) => s.refreshSlow)
  const syncActive = useChatStore((s) => s.syncActive)

  const totalUnread = conversations.reduce((acc, c) => acc + (c.unreadCount ?? 0), 0)
  const pendingRequests = friendRequests.filter((r) => r.status === 0).length

  // 启动：WS + 首屏数据
  useEffect(() => {
    bootstrap()
    void refresh()
    void refreshSlow()
  }, [bootstrap, refresh, refreshSlow])

  // 轮询：会话 5s 一刷；好友/申请 15s 一刷
  useEffect(() => {
    let tick = 0
    const timer = setInterval(() => {
      void refresh()
      void syncActive()
      if (++tick % 3 === 0) void refreshSlow()
    }, 5000)
    return () => clearInterval(timer)
  }, [refresh, syncActive, refreshSlow])

  const switchNav = (next: NavKey): void => {
    if (next === nav) return
    prevNav.current = nav
    setNav(next)
  }

  const doLogout = (): void => {
    setConfirmLogout(false)
    reset()
    logout()
    toast('已退出登录', 'info')
  }

  const direction = NAV_ORDER.indexOf(nav) >= NAV_ORDER.indexOf(prevNav.current) ? 1 : -1

  return (
    <motion.div
      className="shell"
      initial={{ opacity: 0, scale: 1.012, y: 10 }}
      animate={{ opacity: 1, scale: 1, y: 0 }}
      exit={{ opacity: 0, y: 14, scale: 0.995 }}
      transition={{ duration: 0.3, ease: 'easeOut' }}
    >
      <TitleBar />

      <motion.aside
        className="rail"
        initial={{ x: -30, opacity: 0 }}
        animate={{ x: 0, opacity: 1 }}
        transition={{ type: 'spring', stiffness: 300, damping: 28, delay: 0.04 }}
      >
        <button className="rail-avatar" title="我的资料" onClick={() => switchNav('me')}>
          <Avatar name={profile?.nickName || '我'} url={profile?.avatarUrl} size={40} online />
          {nav !== 'me' && <span className="rail-avatar-ring" />}
        </button>

        <div className="rail-nav">
          {NAV_ITEMS.map((item) => {
            const Icon = item.icon
            const badge =
              item.key === 'chat' ? totalUnread : item.key === 'contacts' ? pendingRequests : 0
            return (
              <button
                key={item.key}
                className="rail-item"
                title={item.label}
                onClick={() => switchNav(item.key)}
              >
                {nav === item.key && (
                  <motion.span
                    layoutId="rail-active-pill"
                    className="rail-active-pill"
                    transition={{ type: 'spring', stiffness: 380, damping: 32 }}
                  />
                )}
                <motion.span
                  className="rail-icon"
                  whileHover={{ scale: 1.12 }}
                  whileTap={{ scale: 0.92 }}
                >
                  <Icon width={21} height={21} />
                </motion.span>
                {badge > 0 && (
                  <motion.span
                    key={badge}
                    className="rail-badge"
                    initial={{ scale: 0.4 }}
                    animate={{ scale: [0.4, 1.25, 1] }}
                    transition={{ duration: 0.35 }}
                  >
                    {badge > 99 ? '99+' : badge}
                  </motion.span>
                )}
                <span className="rail-label">{item.label}</span>
              </button>
            )
          })}
        </div>

        <div className="rail-bottom">
          <button className="rail-item" title="退出登录" onClick={() => setConfirmLogout(true)}>
            <motion.span
              className="rail-icon"
              whileHover={{ scale: 1.12 }}
              whileTap={{ scale: 0.92 }}
            >
              <LogoutIcon width={20} height={20} />
            </motion.span>
            <span className="rail-label">退出</span>
          </button>
        </div>
      </motion.aside>

      <main className="shell-body">
        <AnimatePresence mode="wait" custom={direction}>
          <motion.div
            key={nav}
            className="shell-view"
            custom={direction}
            initial="enter"
            animate="center"
            exit="exit"
            variants={{
              enter: (dir: number) => ({ opacity: 0, x: 44 * dir, scale: 0.995 }),
              center: { opacity: 1, x: 0, scale: 1 },
              exit: (dir: number) => ({ opacity: 0, x: -44 * dir, scale: 0.995 })
            }}
            transition={{ duration: 0.24, ease: [0.32, 0.72, 0, 1] }}
          >
            {nav === 'chat' && <ChatView onGoContacts={() => switchNav('contacts')} />}
            {nav === 'contacts' && <ContactsView onNav={switchNav} />}
            {nav === 'me' && <MeView />}
          </motion.div>
        </AnimatePresence>
      </main>

      <div className="status-dock" title={wsStatus === 'open' ? '已连接' : '连接中…'}>
        <ConnectionDot ok={wsStatus === 'open'} />
      </div>

      <AnimatePresence>
        {confirmLogout && (
          <motion.div
            className="modal-overlay"
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            onMouseDown={(e) => {
              if (e.target === e.currentTarget) setConfirmLogout(false)
            }}
          >
            <motion.div
              className="modal-card confirm-card"
              initial={{ opacity: 0, scale: 0.9, y: 12 }}
              animate={{ opacity: 1, scale: 1, y: 0 }}
              exit={{ opacity: 0, scale: 0.94, y: 8 }}
              transition={{ type: 'spring', stiffness: 400, damping: 30 }}
            >
              <h3>退出登录</h3>
              <p>确定要退出当前账号吗？</p>
              <div className="modal-actions">
                <button className="ghost-btn" onClick={() => setConfirmLogout(false)}>
                  取消
                </button>
                <button className="danger-btn" onClick={doLogout}>
                  退出
                </button>
              </div>
            </motion.div>
          </motion.div>
        )}
      </AnimatePresence>
    </motion.div>
  )
}
