import { loadServerSettings } from './settings'
import type { WsFrame, WsMessagePayload } from '@/api/types'

/**
 * WebSocket 长连接客户端。
 *
 * - 心跳：客户端每 25s 发 {"type":"ping"}，服务端回 pong；
 *   服务端也会在协议层每 50s 发 ping，浏览器自动回 pong，无需处理。
 * - 断线：指数退避重连（1s → 30s），页面隐藏时不重连。
 */

export type WsStatus = 'idle' | 'connecting' | 'open' | 'closed'

type MessageHandler = (payload: WsMessagePayload) => void
type StatusHandler = (status: WsStatus) => void
type KickedHandler = (reason: string) => void

class WsClient {
  private ws: WebSocket | null = null
  private token = ''
  private heartbeatTimer: ReturnType<typeof setInterval> | null = null
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null
  private retryCount = 0
  private closedByUser = false
  private messageHandlers = new Set<MessageHandler>()
  private statusHandlers = new Set<StatusHandler>()
  private kickedHandlers = new Set<KickedHandler>()

  get isOpen(): boolean {
    return this.ws?.readyState === WebSocket.OPEN
  }

  onMessage(handler: MessageHandler): () => void {
    this.messageHandlers.add(handler)
    return () => this.messageHandlers.delete(handler)
  }

  onStatus(handler: StatusHandler): () => void {
    this.statusHandlers.add(handler)
    return () => this.statusHandlers.delete(handler)
  }

  onKicked(handler: KickedHandler): () => void {
    this.kickedHandlers.add(handler)
    return () => this.kickedHandlers.delete(handler)
  }

  private emitStatus(status: WsStatus): void {
    for (const h of this.statusHandlers) h(status)
  }

  connect(token: string): void {
    if (!token) return
    // 同一令牌且连接已在途/已建立时忽略重复调用（如 React StrictMode 双挂载）
    if (this.token === token && this.ws && this.ws.readyState <= WebSocket.OPEN) return
    this.token = token
    this.closedByUser = false
    this.cleanupTimers()

    const { wsBase } = loadServerSettings()
    const url = `${wsBase}${wsBase.includes('?') ? '&' : '?'}token=${encodeURIComponent(token)}`

    this.emitStatus('connecting')
    const ws = new WebSocket(url)
    this.ws = ws

    ws.onopen = () => {
      this.retryCount = 0
      this.emitStatus('open')
      this.startHeartbeat()
    }

    ws.onmessage = (event) => {
      try {
        const frame = JSON.parse(event.data as string) as WsFrame
        if (frame.type === 'message') {
          const payload = frame.data as WsMessagePayload
          for (const h of this.messageHandlers) h(payload)
        }
        if (frame.type === 'kicked') {
          this.closedByUser = true
          this.token = ''
          this.cleanupTimers()
          const reason = typeof frame.data === 'string' ? frame.data : '登录状态已失效，请重新登录'
          const active = this.ws
          this.ws = null
          active?.close()
          this.emitStatus('closed')
          for (const h of this.kickedHandlers) h(reason)
        }
        // pong / error：心跳维持静默，错误帧由业务层触发刷新兜底
      } catch {
        /* 忽略无法解析的帧 */
      }
    }

    ws.onclose = () => {
      this.stopHeartbeat()
      this.emitStatus('closed')
      if (!this.closedByUser) this.scheduleReconnect()
    }

    ws.onerror = () => {
      /* onclose 会跟随触发，统一在 onclose 处理 */
    }
  }

  /** 主动关闭（登出）：不再重连 */
  close(): void {
    this.closedByUser = true
    this.token = ''
    this.cleanupTimers()
    this.ws?.close()
    this.ws = null
    this.emitStatus('idle')
  }

  /** 重连（手动/配置变更后） */
  reconnect(): void {
    if (this.token) {
      this.retryCount = 0
      this.ws?.close()
    }
  }

  /** 发送 JSON 帧；连接不可用时返回 false 由调用方降级为 HTTP */
  send(payload: unknown): boolean {
    if (!this.isOpen) return false
    try {
      this.ws?.send(JSON.stringify(payload))
      return true
    } catch {
      return false
    }
  }

  private startHeartbeat(): void {
    this.stopHeartbeat()
    this.heartbeatTimer = setInterval(() => {
      this.send({ type: 'ping' })
    }, 25_000)
  }

  private stopHeartbeat(): void {
    if (this.heartbeatTimer) {
      clearInterval(this.heartbeatTimer)
      this.heartbeatTimer = null
    }
  }

  private scheduleReconnect(): void {
    if (this.reconnectTimer) return
    const delay = Math.min(1000 * 2 ** this.retryCount, 30_000)
    this.retryCount += 1
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null
      if (this.token && !this.closedByUser) {
        this.connect(this.token)
      }
    }, delay)
  }

  private cleanupTimers(): void {
    this.stopHeartbeat()
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer)
      this.reconnectTimer = null
    }
  }
}

export const wsClient = new WsClient()
