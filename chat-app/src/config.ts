export type Discovery = 'lb' | 'direct'

export interface ChatConfig {
  discovery: Discovery
  lbUrl: string
  historyUrl: string
  wsPath: string
}

declare global {
  interface Window {
    __CHAT_CONFIG__?: Partial<ChatConfig>
  }
}

const defaults: ChatConfig = {
  discovery: 'lb',
  lbUrl: '/lb/get',
  historyUrl: '/api/history',
  wsPath: '/ws',
}

export const config: ChatConfig = { ...defaults, ...window.__CHAT_CONFIG__ }
