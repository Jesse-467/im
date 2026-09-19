/**
 * 服务器地址配置。
 *
 * 纯模块（不依赖 React/zustand），渲染层任意位置都能同步读写；
 * 持久化在 localStorage。默认指向本地 docker-compose 起的后端端口：
 * account 8001 / chat 8002（见仓库根 .env.example）。
 */

export interface ServerSettings {
  accountBase: string
  chatBase: string
  wsBase: string
}

export const DEFAULT_SERVER_SETTINGS: ServerSettings = {
  accountBase: 'http://127.0.0.1:8001',
  chatBase: 'http://127.0.0.1:8002',
  wsBase: 'ws://127.0.0.1:8002/ws'
}

const KEY = 'im.settings.server'

function normalize(url: string, fallback: string): string {
  const trimmed = url.trim().replace(/\/+$/, '')
  return trimmed || fallback
}

export function loadServerSettings(): ServerSettings {
  try {
    const raw = localStorage.getItem(KEY)
    if (!raw) return { ...DEFAULT_SERVER_SETTINGS }
    const parsed = JSON.parse(raw) as Partial<ServerSettings>
    return {
      accountBase: normalize(parsed.accountBase ?? '', DEFAULT_SERVER_SETTINGS.accountBase),
      chatBase: normalize(parsed.chatBase ?? '', DEFAULT_SERVER_SETTINGS.chatBase),
      wsBase: normalize(parsed.wsBase ?? '', DEFAULT_SERVER_SETTINGS.wsBase)
    }
  } catch {
    return { ...DEFAULT_SERVER_SETTINGS }
  }
}

export function saveServerSettings(next: ServerSettings): ServerSettings {
  const normalized: ServerSettings = {
    accountBase: normalize(next.accountBase, DEFAULT_SERVER_SETTINGS.accountBase),
    chatBase: normalize(next.chatBase, DEFAULT_SERVER_SETTINGS.chatBase),
    wsBase: normalize(next.wsBase, DEFAULT_SERVER_SETTINGS.wsBase)
  }
  localStorage.setItem(KEY, JSON.stringify(normalized))
  return normalized
}
