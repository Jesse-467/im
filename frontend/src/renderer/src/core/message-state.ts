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

export function confirmMessage(existing: LocalMsg[], uuid: string, receipt: {
  id: string; seq: number; createTime: number
}): LocalMsg[] {
  return existing.map((m) => m.uuid === uuid ? { ...m, ...receipt, pending: false, failed: false } : m)
}

/** 请求失败可能晚于 WS 成功回执到达，不得把已确认的消息重新标为失败。 */
export function failMessage(existing: LocalMsg[], uuid: string): LocalMsg[] {
  return existing.map((m) => m.uuid === uuid && m.pending && m.id === ''
    ? { ...m, pending: false, failed: true }
    : m)
}

export function prepareRetry(existing: LocalMsg[], uuid: string): LocalMsg[] {
  return existing.map((m) => m.uuid === uuid && m.failed && !m.pending && m.id === ''
    ? { ...m, pending: true, failed: false }
    : m)
}
