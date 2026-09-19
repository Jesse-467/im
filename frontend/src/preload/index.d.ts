import type { IpcRendererEvent } from 'electron'

declare global {
  interface Window {
    bridge: {
      /**
       * 桌面（Electron preload 注入）时不存在；
       * 非 Electron 环境（安卓 WebView / 浏览器）由 bridge-fallback 注入直连实现并置 true，
       * UI 据此隐藏桌面专属控件（窗口控制按钮等）。
       */
      readonly __direct?: true
      http: {
        fetch(opts: {
          url: string
          method?: 'GET' | 'POST'
          headers?: Record<string, string>
          body?: string
          timeoutMs?: number
        }): Promise<{ ok: boolean; status: number; body: string; error?: string }>
      }
      window: {
        minimize(): void
        maximize(): void
        close(): void
        onMaximizedChanged(cb: (maximized: boolean) => void): () => void
      }
    }
  }
}

export {}
