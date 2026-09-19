/**
 * 登录态（JWT）的进程内缓存 + localStorage 持久化。
 *
 * 独立于 zustand store 存在：api/client.ts 需要同步读取 token，
 * 而 auth store 又要调用 client，拆成纯模块避免循环依赖。
 */

const KEY = 'im.token'

let cached = ''

try {
  cached = localStorage.getItem(KEY) ?? ''
} catch {
  cached = ''
}

export function getToken(): string {
  return cached
}

export function setToken(token: string): void {
  cached = token
  try {
    if (token) localStorage.setItem(KEY, token)
    else localStorage.removeItem(KEY)
  } catch {
    /* 存储不可用时静默降级为内存态 */
  }
}
