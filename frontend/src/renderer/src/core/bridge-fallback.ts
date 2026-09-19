/**
 * 渲染层的 bridge 兜底。
 *
 * 正式运行环境里 preload 脚本先于页面脚本执行，window.bridge 一定存在；
 * 这里只为「脱离 Electron 直接用浏览器打开渲染层」的场景提供空实现，
 * 避免启动即崩溃，也让 UI 可以在浏览器里独立开发预览。
 */
export function installBridgeFallback(): void {
  if (window.bridge) return

  console.warn('[im] window.bridge 未注入，当前为非 Electron 环境，使用空实现兜底')

  window.bridge = {
    http: {
      fetch: async () => ({ ok: false, status: 0, body: '', error: 'bridge unavailable' })
    },
    window: {
      minimize: () => undefined,
      maximize: () => undefined,
      close: () => undefined,
      onMaximizedChanged: () => () => undefined
    }
  }
}
