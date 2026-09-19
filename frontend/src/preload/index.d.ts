import type { IpcRendererEvent } from 'electron'

declare global {
  interface Window {
    bridge: {
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
