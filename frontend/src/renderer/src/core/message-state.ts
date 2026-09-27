import type { ChatMsg } from '../api/types'

export interface LocalMsg {
  id: string
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

export function toLocalMsg(m: ChatMsg): LocalMsg {
  return {
    ...m,
    uuid: m.uuid || `id-${m.id}`,
    pending: false,
    failed: false,
    recalled: false
  }
}

/** 历史页、补齐页和乐观发送共享同一合并规则；回执只更新本地项，不推进补齐游标。 */
export function mergeMessages(existing: LocalMsg[], incoming: ChatMsg[]): LocalMsg[] {
  const merged = [...existing]
  for (const msg of incoming) {
    const index = merged.findIndex((m) => (m.id !== '' && m.id === msg.id) || (msg.uuid !== '' && m.uuid === msg.uuid))
    if (index < 0) merged.push(toLocalMsg(msg))
    else merged[index] = { ...toLocalMsg(msg), recalled: merged[index].recalled }
  }
  // 尚未确认的消息没有 seq，留在尾部，不挤到历史消息前面。
  return merged.sort((a, b) => (a.seq || Infinity) - (b.seq || Infinity) || a.createTime - b.createTime)
}

export function receivedCursor(fromSeq: number, incoming: ChatMsg[]): number {
  return incoming.reduce((cursor, m) => Math.max(cursor, m.seq), fromSeq)
}
