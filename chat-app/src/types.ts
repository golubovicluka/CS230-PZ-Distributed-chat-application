export interface ChatMessage {
  id: number
  username: string
  content: string
  server?: string
  timestamp?: string
}

export type ConnectionState = 'connecting' | 'open' | 'reconnecting' | 'closed'
