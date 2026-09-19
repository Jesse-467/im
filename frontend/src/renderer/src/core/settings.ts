/**
 * 服务器地址配置。
 *
 * 纯模块（不依赖 React/zustand），渲染层任意位置都能同步读写。
 *
 * 默认值优先级：
 *   1. 构建期环境变量（VITE_ACCOUNT_BASE / VITE_CHAT_BASE / VITE_WS_BASE）
 *      —— 部署到阿里云等公网环境时，构建前注入即可切换，本地调试无需配置；
 *   2. 本地回环地址（仓库根 docker-compose 起的后端端口：account 8001 / chat 8002）。
 *
 * UI 设置页保存的地址持久化在 localStorage，优先级高于上述默认值；
 * 「恢复默认」会清除该覆盖，重新跟随环境变量。
 */

export interface ServerSettings {
  accountBase: string
  chatBase: string
  wsBase: string
}

/** 构建期环境变量注入的默认值；未配置时回退本地回环地址 */
export const DEFAULT_SERVER_SETTINGS: ServerSettings = {
  accountBase: import.meta.env.VITE_ACCOUNT_BASE || 'http://127.0.0.1:8001',
  chatBase: import.meta.env.VITE_CHAT_BASE || 'http://127.0.0.1:8002',
  wsBase: import.meta.env.VITE_WS_BASE || 'ws://127.0.0.1:8002/ws'
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

/** 清除 UI 层的手动覆盖，恢复跟随构建期环境变量 */
export function resetServerSettings(): ServerSettings {
  localStorage.removeItem(KEY)
  return { ...DEFAULT_SERVER_SETTINGS }
}
