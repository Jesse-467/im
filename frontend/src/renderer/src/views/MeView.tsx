import { useState } from 'react'
import { motion } from 'framer-motion'
import { useAuthStore } from '@/store/auth'
import { useChatStore } from '@/store/chat'
import { ApiError } from '@/api/client'
import { toast } from '@/store/toast'
import { Avatar } from '@/components/Avatar'
import { ServerSettingsModal } from './AuthView'
import { wsClient } from '@/core/ws'
import { MailIcon, UserIcon, ServerIcon, RefreshIcon, LogoutIcon, ClockIcon } from '@/components/icons'

/** 设置页：资料卡 + 服务器与连接信息 */
export function MeView(): JSX.Element {
  const profile = useAuthStore((s) => s.profile)
  const updateProfile = useAuthStore((s) => s.updateProfile)
  const wsStatus = useChatStore((s) => s.wsStatus)

  const [editing, setEditing] = useState(false)
  const [nickName, setNickName] = useState(profile?.nickName ?? '')
  const [gender, setGender] = useState(profile?.gender ?? 0)
  const [busy, setBusy] = useState(false)
  const [serverOpen, setServerOpen] = useState(false)

  const startEdit = (): void => {
    setNickName(profile?.nickName ?? '')
    setGender(profile?.gender ?? 0)
    setEditing(true)
  }

  const save = async (): Promise<void> => {
    if (!nickName.trim()) {
      toast('昵称不能为空', 'error')
      return
    }
    if (busy) return
    setBusy(true)
    try {
      await updateProfile({
        nickName: nickName.trim(),
        gender,
        avatarUrl: profile?.avatarUrl ?? ''
      })
      setEditing(false)
    } catch (err) {
      toast(err instanceof ApiError ? err.message : '保存失败', 'error')
    } finally {
      setBusy(false)
    }
  }

  const genderText = gender === 1 ? '男' : gender === 2 ? '女' : '保密'

  return (
    <div className="me-layout">
      <motion.section
        className="panel me-panel"
        initial={{ opacity: 0, y: 14 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ type: 'spring', stiffness: 280, damping: 30 }}
      >
        <div className="me-hero">
          <div className="me-avatar-glow">
            <Avatar
              name={profile?.nickName || '我'}
              url={profile?.avatarUrl}
              size={72}
              online
            />
          </div>
          <div className="me-hero-text">
            <h2>{profile?.nickName || '未命名'}</h2>
            <div className="me-tags">
              <span className="me-tag">ID {profile?.userId ?? '-'}</span>
              <span className="me-tag">{genderText}</span>
            </div>
          </div>
          {!editing && (
            <motion.button
              className="icon-chip me-edit-chip"
              title="编辑资料"
              whileHover={{ y: -1, rotate: -4 }}
              whileTap={{ scale: 0.92 }}
              onClick={startEdit}
            >
              <UserIcon width={15} height={15} />
            </motion.button>
          )}
        </div>

        <div className="me-fields">
          <div className="me-field">
            <MailIcon width={15} height={15} />
            <span className="me-field-label">邮箱</span>
            <span className="me-field-value">{profile?.email || '-'}</span>
          </div>
          <div className="me-field">
            <UserIcon width={15} height={15} />
            <span className="me-field-label">昵称</span>
            {editing ? (
              <input
                className="me-input"
                value={nickName}
                maxLength={24}
                onChange={(e) => setNickName(e.target.value)}
                autoFocus
              />
            ) : (
              <span className="me-field-value">{profile?.nickName || '-'}</span>
            )}
          </div>
          <div className="me-field">
            <span className="me-field-glyph">{gender === 2 ? '♀' : gender === 1 ? '♂' : '·'}</span>
            <span className="me-field-label">性别</span>
            {editing ? (
              <div className="gender-options">
                {[
                  { v: 0, label: '保密' },
                  { v: 1, label: '男' },
                  { v: 2, label: '女' }
                ].map((g) => (
                  <button
                    key={g.v}
                    className={gender === g.v ? 'gender-opt active' : 'gender-opt'}
                    onClick={() => setGender(g.v)}
                  >
                    {g.label}
                  </button>
                ))}
              </div>
            ) : (
              <span className="me-field-value">{genderText}</span>
            )}
          </div>
        </div>

        {editing && (
          <motion.div
            className="me-edit-actions"
            initial={{ opacity: 0, height: 0 }}
            animate={{ opacity: 1, height: 'auto' }}
            exit={{ opacity: 0, height: 0 }}
          >
            <button className="ghost-btn" onClick={() => setEditing(false)}>
              取消
            </button>
            <button className="primary-btn" disabled={busy} onClick={() => void save()}>
              {busy ? '保存中…' : '保存资料'}
            </button>
          </motion.div>
        )}
      </motion.section>

      <motion.section
        className="panel me-panel me-panel-2"
        initial={{ opacity: 0, y: 18 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ type: 'spring', stiffness: 280, damping: 30, delay: 0.06 }}
      >
        <h3 className="me-section-title">
          <ServerIcon width={15} height={15} />
          服务与连接
        </h3>
        <button className="me-row" onClick={() => setServerOpen(true)}>
          <div>
            <div className="me-row-title">服务器地址</div>
            <div className="me-row-sub">Account / Chat / WebSocket</div>
          </div>
          <span className="me-row-go">›</span>
        </button>
        <div className="me-row static">
          <div>
            <div className="me-row-title">实时通道</div>
            <div className="me-row-sub">
              {wsStatus === 'open'
                ? '已连接 · 消息实时推送中'
                : wsStatus === 'connecting'
                  ? '连接中…'
                  : '未连接 · 消息将通过 HTTP 轮询收发'}
            </div>
          </div>
          <span className={`ws-pill ${wsStatus === 'open' ? 'ok' : 'off'}`}>
            {wsStatus === 'open' ? '在线' : '离线'}
          </span>
        </div>
        <div className="me-row static">
          <div>
            <div className="me-row-title">客户端</div>
            <div className="me-row-sub">微语 IM · Electron 跨平台客户端</div>
          </div>
          <span className="ws-pill ok">
            <ClockIcon width={11} height={11} />
            v0.1.0
          </span>
        </div>
        <button
          className="me-row"
          onClick={() => {
            wsClient.reconnect()
            toast('正在重连实时通道…', 'info')
          }}
        >
          <div>
            <div className="me-row-title">重新连接</div>
            <div className="me-row-sub">手动重建 WebSocket 长连接</div>
          </div>
          <motion.span whileTap={{ rotate: 180 }} transition={{ duration: 0.4 }} className="me-row-go">
            <RefreshIcon width={14} height={14} />
          </motion.span>
        </button>
        <button className="me-row danger-row" onClick={() => window.bridge.window.close()}>
          <div>
            <div className="me-row-title">关闭应用</div>
            <div className="me-row-sub">退出客户端</div>
          </div>
          <LogoutIcon width={15} height={15} />
        </button>
      </motion.section>

      <ServerSettingsModal open={serverOpen} onClose={() => setServerOpen(false)} />
    </div>
  )
}
