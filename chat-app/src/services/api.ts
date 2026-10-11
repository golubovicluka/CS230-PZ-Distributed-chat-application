import { config } from '@/config'
import type { ChatMessage } from '@/types'

interface ServerInfo {
  Address: string
  Load: number
}

function sameOriginWebSocketUrl(path: string): string {
  const scheme = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${scheme}//${window.location.host}${path}`
}

export function webSocketUrlFor(address: string): string {
  const base = address.replace(/\/+$/, '')
  if (base.startsWith('/')) {
    return sameOriginWebSocketUrl(`${base}/ws`)
  }
  return `${base}/ws`
}

export async function resolveWebSocketUrl(): Promise<string> {
  if (config.discovery === 'direct') {
    return sameOriginWebSocketUrl(config.wsPath)
  }

  const response = await fetch(config.lbUrl, { cache: 'no-store' })
  if (!response.ok) {
    throw new Error(`load balancer returned ${response.status}`)
  }
  const server: ServerInfo = await response.json()
  return webSocketUrlFor(server.Address)
}

export async function getChatHistory(): Promise<ChatMessage[]> {
  const response = await fetch(config.historyUrl, { cache: 'no-store' })
  if (!response.ok) {
    throw new Error(`history returned ${response.status}`)
  }
  const messages: ChatMessage[] = await response.json()
  return messages ?? []
}
