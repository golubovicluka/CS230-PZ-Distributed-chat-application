import type { ChatMessage, ConnectionState } from '@/types'

const GOING_AWAY = 1001
const NORMAL_CLOSURE = 1000
const BASE_DELAY_MS = 500
const MAX_DELAY_MS = 10_000
const GOING_AWAY_DELAY_MS = 500

interface Handlers {
  onMessage: (message: ChatMessage) => void
  onStateChange: (state: ConnectionState) => void
  onOpen: () => void
}

export class ChatConnection {
  private ws: WebSocket | null = null
  private timer: ReturnType<typeof setTimeout> | null = null
  private attempt = 0
  private stopped = false
  private readonly resolveUrl: () => Promise<string>
  private readonly username: string
  private readonly handlers: Handlers

  constructor(resolveUrl: () => Promise<string>, username: string, handlers: Handlers) {
    this.resolveUrl = resolveUrl
    this.username = username
    this.handlers = handlers
  }

  start() {
    this.stopped = false
    this.attempt = 0
    void this.connect()
  }

  stop() {
    this.stopped = true
    if (this.timer) {
      clearTimeout(this.timer)
      this.timer = null
    }
    this.ws?.close(NORMAL_CLOSURE)
    this.ws = null
    this.handlers.onStateChange('closed')
  }

  send(content: string): boolean {
    if (this.ws?.readyState !== WebSocket.OPEN) {
      return false
    }
    this.ws.send(JSON.stringify({ content }))
    return true
  }

  private async connect() {
    this.handlers.onStateChange(this.attempt === 0 ? 'connecting' : 'reconnecting')

    let url: string
    try {
      url = await this.resolveUrl()
    } catch (error) {
      console.warn('Could not resolve a chat server:', error)
      this.scheduleReconnect(false)
      return
    }
    if (this.stopped) {
      return
    }

    const ws = new WebSocket(`${url}?username=${encodeURIComponent(this.username)}`)
    this.ws = ws

    ws.onopen = () => {
      this.attempt = 0
      this.handlers.onStateChange('open')
      this.handlers.onOpen()
    }

    ws.onmessage = (event) => {
      try {
        this.handlers.onMessage(JSON.parse(event.data) as ChatMessage)
      } catch (error) {
        console.error('Discarding malformed chat message:', error)
      }
    }

    ws.onclose = (event) => {
      if (this.ws !== ws) {
        return
      }
      this.ws = null
      if (!this.stopped) {
        this.scheduleReconnect(event.code === GOING_AWAY)
      }
    }
  }

  // Full-jitter exponential backoff spreads reconnects out, so a server that
  // drains many clients at once does not stampede the next one.
  private scheduleReconnect(serverIsLeaving: boolean) {
    const ceiling = serverIsLeaving
      ? GOING_AWAY_DELAY_MS
      : Math.min(MAX_DELAY_MS, BASE_DELAY_MS * 2 ** this.attempt)
    const delay = Math.random() * ceiling
    this.attempt += 1
    this.handlers.onStateChange('reconnecting')
    this.timer = setTimeout(() => {
      this.timer = null
      void this.connect()
    }, delay)
  }
}
