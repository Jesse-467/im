/**
 * 渲染层的直连 bridge 兜底。
 *
 * Electron 环境里 preload 脚本先于页面脚本执行，window.bridge 一定存在；
 * 以下场景则由本模块注入「直连实现」（带 __direct 标记，供 UI 判断平台）：
 *
 *   - Capacitor 安卓 APP（WebView）：HTTP 走 CapacitorHttp 原生网络层，
 *     在原生侧发请求，不受 WebView 同源策略（CORS）限制——后端未开
 *     CORS 也能直连；WebSocket 本身不受同源策略约束，直连即可
 *     （后端 WS 网关允许任意 Origin）。
 *   - 浏览器直接打开渲染层（UI 开发预览）：标准 fetch 直连，
 *     需后端开启 CORS，否则仅可用于界面预览。
 */

type BridgeFetchOpts = {
  url: string
  method?: 'GET' | 'POST'
  headers?: Record<string, string>
  body?: string
  timeoutMs?: number
}

type BridgeFetchResult = { ok: boolean; status: number; body: string; error?: string }

/** Capacitor 全局对象（仅安卓/iOS WebView 里存在）的最小类型 */
interface CapacitorGlobal {
  isNativePlatform?: () => boolean
  isPluginAvailable?: (name: string) => boolean
}

/** 用 CapacitorHttp（原生网络层）发请求，绕过 WebView 的 CORS 限制 */
async function fetchViaCapacitor(opts: BridgeFetchOpts): Promise<BridgeFetchResult> {
  const { CapacitorHttp } = await import('@capacitor/core')
  try {
    const res = await CapacitorHttp.request({
      url: opts.url,
      method: opts.method ?? 'POST',
      headers: opts.headers,
      // CapacitorHttp 的 data 字符串会作为请求体原样发出
      data: opts.body ?? ''
    })
    const body = typeof res.data === 'string' ? res.data : JSON.stringify(res.data)
    return { ok: res.status >= 200 && res.status < 300, status: res.status, body }
  } catch (err) {
    const e = err as { status?: number; data?: unknown; message?: string }
    // CapacitorHttp 非 2xx 会抛错，带上状态与响应体，交给上层按业务码处理
    if (typeof e.status === 'number' && e.status > 0) {
      const body =
        typeof e.data === 'string' ? e.data : e.data != null ? JSON.stringify(e.data) : ''
      return { ok: false, status: e.status, body }
    }
    return { ok: false, status: 0, body: '', error: e.message ?? 'network error' }
  }
}

/** 浏览器标准 fetch 直连（后端需开启 CORS） */
async function fetchViaBrowser(opts: BridgeFetchOpts): Promise<BridgeFetchResult> {
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), opts.timeoutMs ?? 10_000)
  try {
    const res = await fetch(opts.url, {
      method: opts.method ?? 'POST',
      headers: opts.headers,
      body: opts.body,
      signal: controller.signal
    })
    const body = await res.text()
    return { ok: res.ok, status: res.status, body }
  } catch (err) {
    const message = err instanceof DOMException && err.name === 'AbortError' ? '请求超时' : '网络错误'
    return { ok: false, status: 0, body: '', error: message }
  } finally {
    clearTimeout(timer)
  }
}

export function installBridgeFallback(): void {
  if (window.bridge) return

  const cap = (window as unknown as { Capacitor?: CapacitorGlobal }).Capacitor
  const isPluginAvailable = cap?.isPluginAvailable
  const hasCapacitorHttp =
    !!cap?.isNativePlatform?.() &&
    typeof isPluginAvailable === 'function' &&
    !!isPluginAvailable.call(cap, 'CapacitorHttp')

  console.warn(
    hasCapacitorHttp
      ? '[im] 非桌面环境：使用 Capacitor 原生网络层直连后端'
      : '[im] window.bridge 未注入：使用浏览器 fetch 直连（后端需开启 CORS）'
  )

  window.bridge = {
    __direct: true,
    http: {
      fetch: (opts) => (hasCapacitorHttp ? fetchViaCapacitor(opts) : fetchViaBrowser(opts))
    },
    window: {
      minimize: () => undefined,
      maximize: () => undefined,
      close: () => undefined,
      onMaximizedChanged: () => () => undefined
    }
  }
}
