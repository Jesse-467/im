/**
 * 登录态（JWT）的进程内缓存 + localStorage 持久化。
 *
 * 独立于 zustand store 存在：api/client.ts 需要同步读取 token，
 * 而 auth store 又要调用 client，拆成纯模块避免循环依赖。
 */

const KEY = 'im.token'
const DEVICE_KEY = 'im.device-id'

let cached = ''
let deviceId = ''

try {
  cached = localStorage.getItem(KEY) ?? ''
} catch {
  cached = ''
}

try {
  deviceId = localStorage.getItem(DEVICE_KEY) ?? ''
} catch {
  deviceId = ''
}

if (!deviceId) {
  try {
    deviceId = crypto.randomUUID()
  } catch {
    deviceId = `desktop-${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`
  }
  try {
    localStorage.setItem(DEVICE_KEY, deviceId)
  } catch {
    /* 存储不可用时使用本进程内的设备标识 */
  }
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

/** 同一 Electron 实例内稳定的设备标识，用于服务端多设备计数。 */
export function getDeviceId(): string {
  return deviceId
}
