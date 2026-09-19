import dayjs from 'dayjs'

/**
 * 展示层格式化工具：时间、头像配色等。
 */

export function formatMsgTime(ts: number): string {
  if (!ts) return ''
  const t = dayjs(ts)
  const now = dayjs()
  if (t.isSame(now, 'day')) return t.format('HH:mm')
  if (t.isSame(now.subtract(1, 'day'), 'day')) return `昨天 ${t.format('HH:mm')}`
  if (t.isSame(now, 'year')) return t.format('M月D日 HH:mm')
  return t.format('YYYY年M月D日 HH:mm')
}

/** 会话列表右上角的紧凑时间 */
export function formatListTime(ts: number): string {
  if (!ts) return ''
  const t = dayjs(ts)
  const now = dayjs()
  if (t.isSame(now, 'day')) return t.format('HH:mm')
  if (t.isSame(now.subtract(1, 'day'), 'day')) return '昨天'
  if (t.isSame(now, 'year')) return t.format('M月D日')
  return t.format('YY/M/D')
}

/** 消息流里的日期分隔标签 */
export function formatDayLabel(ts: number): string {
  const t = dayjs(ts)
  const now = dayjs()
  if (t.isSame(now, 'day')) return '今天'
  if (t.isSame(now.subtract(1, 'day'), 'day')) return '昨天'
  if (t.isSame(now, 'year')) return t.format('M月D日')
  return t.format('YYYY年M月D日')
}

/** 头像配色：按名字/ID 稳定散列到一组柔和渐变里 */
const AVATAR_GRADIENTS = [
  ['#5b8cff', '#38c6ff'],
  ['#12b7f5', '#0a7ffb'],
  ['#7c66ff', '#4fc3f7'],
  ['#00c48c', '#00a6a0'],
  ['#ff9f43', '#ff6b6b'],
  ['#f56ca8', '#c86dd7'],
  ['#5f7dff', '#8f6bff'],
  ['#26c2a4', '#4dabf7']
]

export function avatarGradient(seed: string): string {
  let hash = 0
  for (let i = 0; i < seed.length; i++) {
    hash = (hash * 31 + seed.charCodeAt(i)) >>> 0
  }
  const [from, to] = AVATAR_GRADIENTS[hash % AVATAR_GRADIENTS.length]
  return `linear-gradient(135deg, ${from}, ${to})`
}

/** 昵称用于头像角标展示的首字（取第一个字符） */
export function initialOf(name: string): string {
  const trimmed = (name ?? '').trim()
  return trimmed ? Array.from(trimmed)[0].toUpperCase() : '?'
}

export function isValidEmail(email: string): boolean {
  return /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)
}
