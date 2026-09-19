import { useRef, useState } from 'react'
import { motion } from 'framer-motion'
import { useAuthStore } from '@/store/auth'
import { useChatStore } from '@/store/chat'
import { ApiError } from '@/api/client'
import { toast } from '@/store/toast'
import { Avatar } from '@/components/Avatar'
import { ServerSettingsModal } from './AuthView'
import { wsClient } from '@/core/ws'
import { MailIcon, UserIcon, ServerIcon, RefreshIcon, LogoutIcon, ClockIcon, CameraIcon } from '@/components/icons'

/** 头像边长（px）：144 已足够列表与聊天页展示，控制 base64 体积 */
const AVATAR_SIZE = 144

/**
 * 把用户选中的图片压缩为正方形头像 data URL。
 *
 * 没有文件上传接口，头像以 base64 内联在 avatarUrl 字段落库，
 * 因此在渲染层完成「居中裁剪 + 等比缩放 + JPEG 有损压缩」，
 * 把体积控制在 10KB 量级。Electron / 安卓 WebView / 浏览器均可运行。
 */
function compressAvatar(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    if (!file.type.startsWith('image/')) {
      reject(new Error('请选择图片文件'))
      return
    }
    const reader = new FileReader()
    reader.onerror = () => reject(new Error('读取文件失败'))
    reader.onload = () => {
      const img = new Image()
      img.onerror = () => reject(new Error('图片解析失败'))
      img.onload = () => {
        // 居中裁剪为正方形后缩放到目标尺寸
        const side = Math.min(img.width, img.height)
        const sx = (img.width - side) / 2
        const sy = (img.height - side) / 2
        const canvas = document.createElement('canvas')
        canvas.width = AVATAR_SIZE
        canvas.height = AVATAR_SIZE
        const ctx = canvas.getContext('2d')
        if (!ctx) {
          reject(new Error('画布不可用'))
          return
        }
        ctx.drawImage(img, sx, sy, side, side, 0, 0, AVATAR_SIZE, AVATAR_SIZE)
        resolve(canvas.toDataURL('image/jpeg', 0.86))
      }
      img.src = reader.result as string
    }
    reader.readAsDataURL(file)
  })
}

/** 设置页：资料卡 + 服务器与连接信息 */
export function MeView(): JSX.Element {
  const profile = useAuthStore((s) => s.profile)
  const updateProfile = useAuthStore((s) => s.updateProfile)
  const wsStatus = useChatStore((s) => s.wsStatus)

  const [editing, setEditing] = useState(false)
  const [nickName, setNickName] = useState(profile?.nickName ?? '')
  const [gender, setGender] = useState(profile?.gender ?? 0)
  const [avatarDraft, setAvatarDraft] = useState(profile?.avatarUrl ?? '')
  const [busy, setBusy] = useState(false)
  const [serverOpen, setServerOpen] = useState(false)
  const fileRef = useRef<HTMLInputElement>(null)

  const startEdit = (): void => {
    setNickName(profile?.nickName ?? '')
    setGender(profile?.gender ?? 0)
    setAvatarDraft(profile?.avatarUrl ?? '')
    setEditing(true)
  }

  const onPickAvatar = (): void => {
    fileRef.current?.click()
  }

  const onAvatarChange = async (e: React.ChangeEvent<HTMLInputElement>): Promise<void> => {
    const file = e.target.files?.[0]
    e.target.value = '' // 允许连续选择同一文件
    if (!file) return
    try {
      const dataUrl = await compressAvatar(file)
      setAvatarDraft(dataUrl)
    } catch (err) {
      toast(err instanceof Error ? err.message : '图片处理失败', 'error')
    }
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
        avatarUrl: avatarDraft
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
              url={editing ? avatarDraft || undefined : profile?.avatarUrl}
              size={72}
              online
            />
            {editing && (
              <motion.button
                className="me-avatar-cam"
                title="更换头像"
                whileHover={{ scale: 1.08 }}
                whileTap={{ scale: 0.9 }}
                onClick={onPickAvatar}
              >
                <CameraIcon width={13} height={13} />
              </motion.button>
            )}
          </div>
          <input
            ref={fileRef}
            type="file"
            accept="image/*"
            style={{ display: 'none' }}
            onChange={(e) => void onAvatarChange(e)}
          />
          <div className="me-hero-text">
            <h2>{profile?.nickName || '未命名'}</h2>
            <div className="me-tags">
              <span className="me-tag">ID {profile?.userId ?? '-'}</span>
              <span className="me-tag">{genderText}</span>
              {editing && <span className="me-tag">点相机换头像</span>}
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
