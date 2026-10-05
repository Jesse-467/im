import { useEffect, useState } from 'react'
import { apiGroupMemberList, apiInviteGroup, apiUpdateGroup } from '@/api/chat'
import type { Conversation } from '@/api/types'
import { ApiError } from '@/api/client'
import { useAuthStore } from '@/store/auth'
import { useChatStore } from '@/store/chat'
import { toast } from '@/store/toast'
import { Avatar } from '@/components/Avatar'
import { Modal } from '@/components/Modal'
import { CheckIcon } from '@/components/icons'

export function GroupMembersModal({ open, conversation, onClose }: {
  open: boolean
  conversation: Conversation
  onClose: () => void
}): JSX.Element {
  const convId = conversation.conversationId
  const members = useChatStore((s) => s.membersByConv[convId]) ?? []
  const friends = useChatStore((s) => s.friends)
  const meId = useAuthStore((s) => s.userId)
  const me = members.find((m) => m.userId === meId)
  const canManage = me?.role === 2 || me?.role === 1
  const [tab, setTab] = useState<'members' | 'settings' | 'invite'>('members')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [revision, setRevision] = useState(0)
  const [busy, setBusy] = useState(false)
  const [name, setName] = useState('')
  const [avatar, setAvatar] = useState('')
  const [alias, setAlias] = useState('')
  const [picked, setPicked] = useState<string[]>([])
  const [confirmQuit, setConfirmQuit] = useState(false)
  const candidates = friends.filter((f) => !members.some((m) => m.userId === f.userId))

  useEffect(() => {
    if (!open) return
    setTab('members')
    setConfirmQuit(false)
    setPicked([])
  }, [open, convId])

  useEffect(() => {
    if (!open) return
    let cancelled = false
    setLoading(true)
    setError('')
    void apiGroupMemberList(convId).then((result) => {
      if (!cancelled) useChatStore.setState((s) => ({ membersByConv: { ...s.membersByConv, [convId]: result.list ?? [] } }))
    }).catch((err) => {
      if (!cancelled) setError(err instanceof ApiError ? err.message : '成员加载失败，请重试')
    }).finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [open, convId, revision])

  const save = async (): Promise<void> => {
    if (busy) return
    const patch: { name?: string; avatarUrl?: string; aliasName?: string } = {}
    if (canManage) {
      if (!name.trim()) { toast('请填写群名称', 'error'); return }
      if (name.trim() !== conversation.name) patch.name = name.trim()
      if (avatar.trim() !== conversation.avatarUrl) patch.avatarUrl = avatar.trim()
    }
    if (alias.trim() !== (me?.aliasName ?? '')) patch.aliasName = alias.trim()
    if (Object.keys(patch).length === 0) { toast('没有需要保存的修改', 'info'); return }
    setBusy(true)
    try {
      await apiUpdateGroup(convId, patch)
      toast('群资料已保存', 'success')
      setRevision((n) => n + 1)
      await useChatStore.getState().refresh()
      setTab('members')
    } catch (err) { toast(err instanceof ApiError ? err.message : '保存失败，请重试', 'error') }
    finally { setBusy(false) }
  }

  const invite = async (): Promise<void> => {
    if (busy || picked.length === 0) return
    setBusy(true)
    try {
      const result = await apiInviteGroup(convId, picked)
      toast(result.cnt ? '已邀请 ' + result.cnt + ' 位成员' : '所选好友已在群内', 'success')
      setPicked([])
      setRevision((n) => n + 1)
      setTab('members')
      await useChatStore.getState().refresh()
    } catch (err) { toast(err instanceof ApiError ? err.message : '邀请失败，请重试', 'error') }
    finally { setBusy(false) }
  }

  const quit = async (): Promise<void> => {
    if (busy) return
    setBusy(true)
    try { await useChatStore.getState().quitGroup(convId); onClose() }
    catch (err) { toast(err instanceof ApiError ? err.message : '退群失败，请重试', 'error') }
    finally { setBusy(false) }
  }

  return (
    <Modal open={open} title="群聊详情" width={560} onClose={onClose}>
      <div className="group-summary">
        <span title={conversation.name}>{conversation.name}</span>
        <span className="group-summary-count">{loading && members.length === 0 ? '加载中…' : members.length + ' 位成员'}</span>
      </div>
      <div className="group-tabs" role="tablist" aria-label="群聊详情">
        <button role="tab" aria-selected={tab === 'members'} onClick={() => { setTab('members'); setConfirmQuit(false) }}>成员</button>
        <button role="tab" aria-selected={tab === 'settings'} disabled={!me || busy} onClick={() => {
          setName(conversation.name); setAvatar(conversation.avatarUrl); setAlias(me?.aliasName ?? ''); setTab('settings'); setConfirmQuit(false)
        }}>群设置</button>
        {canManage && <button role="tab" aria-selected={tab === 'invite'} disabled={busy} onClick={() => { setTab('invite'); setConfirmQuit(false) }}>邀请好友</button>}
      </div>
      {error && <div className="group-members-error" role="alert">{error} <button onClick={() => setRevision((n) => n + 1)}>重试</button></div>}
      {tab === 'members' && <div className="group-members-list">
        {[...members].sort((a, b) => b.role - a.role || a.nickName.localeCompare(b.nickName)).map((member) => (
          <div className="group-member-row" key={member.userId}>
            <Avatar name={member.aliasName || member.nickName || '用户 ' + member.userId} url={member.avatarUrl} seed={member.userId} size={38} />
            <div className="group-member-main">
              <div className="group-member-name">{member.aliasName || member.nickName || '用户 ' + member.userId}{member.userId === meId && <span>（你）</span>}</div>
              <div className="group-member-id">ID: {member.userId}</div>
            </div>
            <span className={'group-member-role role-' + member.role}>{member.role === 2 ? '群主' : member.role === 1 ? '管理员' : '成员'}</span>
          </div>
        ))}
      </div>}
      {tab === 'settings' && <div className="group-settings">
        {canManage ? <>
          <label className="stack-field"><span>群名称</span><input value={name} maxLength={50} onChange={(e) => setName(e.target.value)} disabled={busy} /></label>
          <label className="stack-field"><span>群头像链接</span><input value={avatar} maxLength={512} placeholder="留空使用默认群头像" onChange={(e) => setAvatar(e.target.value)} disabled={busy} /></label>
        </> : <p className="modal-tip">群名称和头像由群主或管理员修改。</p>}
        <label className="stack-field"><span>我在群里的昵称</span><input value={alias} maxLength={32} placeholder="留空使用个人昵称" onChange={(e) => setAlias(e.target.value)} disabled={busy} /></label>
        <div className="modal-actions"><button className="primary-btn" disabled={busy} onClick={() => void save()}>{busy ? '保存中…' : '保存修改'}</button></div>
        <div className="group-quit">
          {confirmQuit ? <>
            <p>确定退出“{conversation.name}”？退出后将不再收到这个群的消息。</p>
            <div className="modal-actions"><button className="ghost-btn" disabled={busy} onClick={() => setConfirmQuit(false)}>取消</button><button className="danger-btn" disabled={busy} onClick={() => void quit()}>{busy ? '退出中…' : '确认退出群聊'}</button></div>
          </> : <>
            <button className="danger-btn" disabled={busy || me?.role === 2} onClick={() => setConfirmQuit(true)}>退出群聊</button>
            {me?.role === 2 && <p className="modal-tip">你是群主，需要留在群内。暂不支持转让群主。</p>}
          </>}
        </div>
      </div>}
      {tab === 'invite' && canManage && <>
        <p className="modal-tip">选择要邀请的好友，已在群内的好友不会重复显示。</p>
        <div className="member-picker">
          {candidates.length === 0 ? <p className="picker-empty">暂无可邀请的好友</p> : candidates.map((friend) => (
            <button key={friend.userId} className={'member-chip ' + (picked.includes(friend.userId) ? 'picked' : '')} disabled={busy} onClick={() => setPicked((ids) => ids.includes(friend.userId) ? ids.filter((id) => id !== friend.userId) : [...ids, friend.userId])}>
              <Avatar name={friend.remark || friend.nickName} url={friend.avatarUrl} seed={friend.userId} size={26} />
              <span>{friend.remark || friend.nickName}</span>
              {picked.includes(friend.userId) && <CheckIcon width={12} height={12} />}
            </button>
          ))}
        </div>
        <div className="modal-actions"><button className="primary-btn" disabled={busy || picked.length === 0} onClick={() => void invite()}>{busy ? '邀请中…' : '邀请 ' + picked.length + ' 位好友'}</button></div>
      </>}
    </Modal>
  )
}
