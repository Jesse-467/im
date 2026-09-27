import { useEffect, useState } from 'react'
import { apiGroupMemberList } from '@/api/chat'
import type { Conversation } from '@/api/types'
import { ApiError } from '@/api/client'
import { useAuthStore } from '@/store/auth'
import { useChatStore } from '@/store/chat'
import { Avatar } from '@/components/Avatar'
import { Modal } from '@/components/Modal'

export function GroupMembersModal({ open, conversation, onClose }: {
  open: boolean
  conversation: Conversation
  onClose: () => void
}): JSX.Element {
  const members = useChatStore((s) => s.membersByConv[conversation.conversationId]) ?? []
  const meId = useAuthStore((s) => s.userId)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [revision, setRevision] = useState(0)

  useEffect(() => {
    if (!open) return
    let cancelled = false
    setLoading(true)
    setError('')
    void apiGroupMemberList(conversation.conversationId).then((result) => {
      if (cancelled) return
      useChatStore.setState((s) => ({ membersByConv: { ...s.membersByConv, [conversation.conversationId]: result.list ?? [] } }))
    }).catch((err) => {
      if (!cancelled) setError(err instanceof ApiError ? err.message : '成员加载失败，请重试')
    }).finally(() => {
      if (!cancelled) setLoading(false)
    })
    return () => { cancelled = true }
  }, [open, conversation.conversationId, revision])

  return (
    <Modal open={open} title="群成员" width={520} onClose={onClose}>
      <div className="group-summary">
        <span>{conversation.name}</span>
        <span className="group-summary-count">{loading && members.length === 0 ? '加载中…' : `${members.length} 位成员`}</span>
      </div>
      {error && <div className="group-members-error" role="alert">{error} <button onClick={() => setRevision((n) => n + 1)}>重试</button></div>}
      <div className="group-members-list">
        {[...members].sort((a, b) => b.role - a.role || a.nickName.localeCompare(b.nickName)).map((member) => (
          <div className="group-member-row" key={member.userId}>
            <Avatar name={member.aliasName || member.nickName || `用户 ${member.userId}`} url={member.avatarUrl} seed={member.userId} size={38} />
            <div className="group-member-main">
              <div className="group-member-name">{member.aliasName || member.nickName || `用户 ${member.userId}`}{member.userId === meId && <span>（你）</span>}</div>
              <div className="group-member-id">ID: {member.userId}</div>
            </div>
            <span className={`group-member-role role-${member.role}`}>{member.role === 2 ? '群主' : member.role === 1 ? '管理员' : '成员'}</span>
          </div>
        ))}
      </div>
    </Modal>
  )
}
