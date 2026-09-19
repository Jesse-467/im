import { useState } from 'react'
import { motion, AnimatePresence } from 'framer-motion'
import { useAuthStore } from '@/store/auth'
import { ApiError } from '@/api/client'
import { toast } from '@/store/toast'
import { Avatar } from '@/components/Avatar'
import { TitleBar } from '@/components/TitleBar'
import { MailIcon, LockIcon, UserIcon, ServerIcon, SmileIcon } from '@/components/icons'
import { isValidEmail } from '@/core/format'
import { loadServerSettings, saveServerSettings, resetServerSettings, DEFAULT_SERVER_SETTINGS } from '@/core/settings'
import { Modal } from '@/components/Modal'

type AuthTab = 'login' | 'register'

const TABS: { key: AuthTab; label: string }[] = [
  { key: 'login', label: '登录' },
  { key: 'register', label: '注册' }
]

export function AuthView(): JSX.Element {
  const [tab, setTab] = useState<AuthTab>('login')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [nickName, setNickName] = useState('')
  const [gender, setGender] = useState(0)
  const [busy, setBusy] = useState(false)
  const [serverOpen, setServerOpen] = useState(false)

  const login = useAuthStore((s) => s.login)
  const register = useAuthStore((s) => s.register)

  const switchTab = (next: AuthTab): void => {
    if (next === tab) return
    setTab(next)
    setPassword('')
    setConfirm('')
  }

  const submit = async (): Promise<void> => {
    if (busy) return
    if (!isValidEmail(email)) {
      toast('请输入正确的邮箱地址', 'error')
      return
    }
    if (password.length < 6) {
      toast('密码至少 6 位', 'error')
      return
    }
    if (tab === 'register') {
      if (!nickName.trim()) {
        toast('请填写昵称', 'error')
        return
      }
      if (password !== confirm) {
        toast('两次输入的密码不一致', 'error')
        return
      }
    }

    setBusy(true)
    try {
      if (tab === 'login') {
        await login(email, password)
        toast('欢迎回来', 'success')
      } else {
        await register({ email, password, nickName: nickName.trim(), gender })
      }
    } catch (err) {
      toast(err instanceof ApiError ? err.message : '操作失败，请稍后重试', 'error')
    } finally {
      setBusy(false)
    }
  }

  return (
    <motion.div
      className="auth-view"
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      exit={{ opacity: 0, y: -16, scale: 0.985 }}
      transition={{ duration: 0.28, ease: 'easeOut' }}
    >
      <TitleBar />

      <div className="auth-stage">
        <div className="auth-blob auth-blob-a" />
        <div className="auth-blob auth-blob-b" />
        <div className="auth-blob auth-blob-c" />

        <motion.div
          className="auth-card"
        initial={{ opacity: 0, y: 26, scale: 0.97 }}
        animate={{ opacity: 1, y: 0, scale: 1 }}
        transition={{ type: 'spring', stiffness: 260, damping: 26, delay: 0.05 }}
      >
        <div className="auth-brand">
          <Avatar name="微" seed="brand" size={56} />
          <div className="auth-brand-text">
            <h1>微语</h1>
            <p>轻盈沟通，即时抵达</p>
          </div>
        </div>

        <nav className="auth-tabs">
          {TABS.map((t) => (
            <button key={t.key} className="auth-tab" onClick={() => switchTab(t.key)}>
              {tab === t.key && (
                <motion.span
                  layoutId="auth-tab-pill"
                  className="auth-tab-pill"
                  transition={{ type: 'spring', stiffness: 420, damping: 34 }}
                />
              )}
              <span className={tab === t.key ? 'auth-tab-text active' : 'auth-tab-text'}>
                {t.label}
              </span>
            </button>
          ))}
        </nav>

        <AnimatePresence mode="wait">
          <motion.div
            key={tab}
            initial={{ opacity: 0, x: tab === 'login' ? -14 : 14 }}
            animate={{ opacity: 1, x: 0 }}
            exit={{ opacity: 0, x: tab === 'login' ? 14 : -14 }}
            transition={{ duration: 0.2, ease: 'easeOut' }}
            className="auth-form"
          >
            <label className="field">
              <MailIcon width={16} height={16} />
              <input
                type="email"
                placeholder="邮箱"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                onKeyDown={(e) => e.key === 'Enter' && void submit()}
                autoFocus
              />
            </label>

            <AnimatePresence initial={false}>
              {tab === 'register' && (
                <motion.div
                  initial={{ opacity: 0, height: 0 }}
                  animate={{ opacity: 1, height: 'auto' }}
                  exit={{ opacity: 0, height: 0 }}
                  transition={{ duration: 0.22 }}
                  style={{ overflow: 'hidden' }}
                >
                  <label className="field">
                    <UserIcon width={16} height={16} />
                    <input
                      type="text"
                      placeholder="昵称"
                      value={nickName}
                      maxLength={24}
                      onChange={(e) => setNickName(e.target.value)}
                    />
                  </label>
                  <div className="field gender-field">
                    <SmileIcon width={16} height={16} />
                    <div className="gender-options">
                      {[
                        { v: 0, label: '保密' },
                        { v: 1, label: '男' },
                        { v: 2, label: '女' }
                      ].map((g) => (
                        <button
                          key={g.v}
                          type="button"
                          className={gender === g.v ? 'gender-opt active' : 'gender-opt'}
                          onClick={() => setGender(g.v)}
                        >
                          {g.label}
                        </button>
                      ))}
                    </div>
                  </div>
                </motion.div>
              )}
            </AnimatePresence>

            <label className="field">
              <LockIcon width={16} height={16} />
              <input
                type="password"
                placeholder="密码"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                onKeyDown={(e) => e.key === 'Enter' && void submit()}
              />
            </label>

            {tab === 'register' && (
              <label className="field">
                <LockIcon width={16} height={16} />
                <input
                  type="password"
                  placeholder="确认密码"
                  value={confirm}
                  onChange={(e) => setConfirm(e.target.value)}
                  onKeyDown={(e) => e.key === 'Enter' && void submit()}
                />
              </label>
            )}

            <motion.button
              className={`primary-btn ${busy ? 'busy' : ''}`}
              whileHover={{ y: -1 }}
              whileTap={{ scale: 0.97 }}
              onClick={() => void submit()}
              disabled={busy}
            >
              {busy ? '请稍候…' : tab === 'login' ? '登 录' : '注 册'}
            </motion.button>
          </motion.div>
        </AnimatePresence>

        <footer className="auth-footer">
          <button className="ghost-btn" onClick={() => setServerOpen(true)}>
            <ServerIcon width={14} height={14} />
            服务器设置
          </button>
        </footer>
        </motion.div>
      </div>

      <ServerSettingsModal open={serverOpen} onClose={() => setServerOpen(false)} />
    </motion.div>
  )
}

/** 服务器地址设置（登录页/我的页共用） */
export function ServerSettingsModal({
  open,
  onClose
}: {
  open: boolean
  onClose: () => void
}): JSX.Element {
  const [settings, setSettings] = useState(loadServerSettings())

  const save = (): void => {
    saveServerSettings(settings)
    toast('服务器地址已保存', 'success')
    onClose()
  }

  /** 清除手动覆盖，恢复跟随构建时的环境变量默认值 */
  const reset = (): void => {
    const restored = resetServerSettings()
    setSettings(restored)
    toast('已恢复默认地址（跟随构建配置）', 'success')
  }

  return (
    <Modal open={open} title="服务器设置" onClose={onClose}>
      <p className="modal-tip">
        默认跟随构建配置（未配置时指向本地后端 Account :8001 / Chat :8002）
      </p>
      <label className="stack-field">
        <span>Account 服务地址</span>
        <input
          value={settings.accountBase}
          onChange={(e) => setSettings({ ...settings, accountBase: e.target.value })}
          placeholder={DEFAULT_SERVER_SETTINGS.accountBase}
        />
      </label>
      <label className="stack-field">
        <span>Chat 服务地址</span>
        <input
          value={settings.chatBase}
          onChange={(e) => setSettings({ ...settings, chatBase: e.target.value })}
          placeholder={DEFAULT_SERVER_SETTINGS.chatBase}
        />
      </label>
      <label className="stack-field">
        <span>WebSocket 地址</span>
        <input
          value={settings.wsBase}
          onChange={(e) => setSettings({ ...settings, wsBase: e.target.value })}
          placeholder={DEFAULT_SERVER_SETTINGS.wsBase}
        />
      </label>
      <div className="modal-actions">
        <button className="ghost-btn" onClick={reset}>
          恢复默认
        </button>
        <button className="primary-btn" onClick={save}>
          保存
        </button>
      </div>
    </Modal>
  )
}
