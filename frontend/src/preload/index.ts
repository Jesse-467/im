import { contextBridge, ipcRenderer, type IpcRendererEvent } from 'electron'

/**
 * 预加载脚本：以最小面暴露主进程能力。
 *
 * - window.* 窗口控制：渲染层自绘标题栏需要
 * - http.fetch：渲染层所有业务请求都走它，绕开后端未开 CORS 的限制
 */
const bridge = {
  http: {
    fetch(opts: {
      url: string
      method?: 'GET' | 'POST'
      headers?: Record<string, string>
      body?: string
      timeoutMs?: number
    }): Promise<{ ok: boolean; status: number; body: string; error?: string }> {
      return ipcRenderer.invoke('http:fetch', opts)
    }
  },
  window: {
    minimize(): void {
      ipcRenderer.send('window:minimize')
    },
    maximize(): void {
      ipcRenderer.send('window:maximize')
    },
    close(): void {
      ipcRenderer.send('window:close')
    },
    onMaximizedChanged(cb: (maximized: boolean) => void): () => void {
      const listener = (_e: IpcRendererEvent, maximized: boolean): void => cb(maximized)
      ipcRenderer.on('window:maximized-changed', listener)
      return () => {
        ipcRenderer.removeListener('window:maximized-changed', listener)
      }
    }
  }
}

export type Bridge = typeof bridge

contextBridge.exposeInMainWorld('bridge', bridge)
