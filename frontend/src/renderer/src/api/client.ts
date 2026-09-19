import { getToken } from '@/core/session'
import { loadServerSettings } from '@/core/settings'
import type { ApiBody } from './types'

/**
 * 业务错误：携带后端统一响应里的 code/msg。
 * 后端约定 HTTP 状态恒为 200，业务结果由 code 表达（0 成功）。
 */
export class ApiError extends Error {
  readonly code: number
  readonly httpStatus: number

  constructor(msg: string, code: number, httpStatus = 0) {
    super(msg || '网络异常，请稍后重试')
    this.name = 'ApiError'
    this.code = code
    this.httpStatus = httpStatus
  }
}

export type ServiceName = 'account' | 'chat'

function baseUrlOf(service: ServiceName): string {
  const settings = loadServerSettings()
  return service === 'account' ? settings.accountBase : settings.chatBase
}

/** 业务码：未认证（Chat/Account 两服务数值语义一致） */
export const CODE_UNAUTHORIZED = 4001
/** 令牌已被吊销（登出、被其他设备挤下线或修改密码）。 */
export const CODE_TOKEN_REVOKED = 4007

/**
 * 发起一次业务 POST 请求。
 *
 * 走 window.bridge（主进程 fetch）而不是渲染层 fetch：
 * 后端未开 CORS，渲染层直连会被同源策略拦截。
 */
export async function post<T>(service: ServiceName, path: string, body?: unknown): Promise<T> {
  const token = getToken()
  const url = baseUrlOf(service) + path

  let res: { ok: boolean; status: number; body: string; error?: string }
  try {
    res = await window.bridge.http.fetch({
      url,
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        ...(token ? { Authorization: `Bearer ${token}` } : {})
      },
      body: JSON.stringify(body ?? {})
    })
  } catch {
    throw new ApiError('无法连接服务器，请检查网络或服务端地址', 0)
  }

  if (res.status === 0) {
    throw new ApiError('无法连接服务器，请检查网络或服务端地址', 0)
  }

  let parsed: ApiBody<T>
  try {
    parsed = JSON.parse(res.body) as ApiBody<T>
  } catch {
    throw new ApiError(`服务响应异常（HTTP ${res.status}）`, 0, res.status)
  }

  if (!res.ok || parsed.code !== 0) {
    throw new ApiError(parsed.msg, parsed.code, res.status)
  }
  return parsed.data
}
