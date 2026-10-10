import { useCallback, useEffect, useRef, useState } from 'react'
import './App.css'
import { Login } from './components/Login'
import { ChatRoom } from './components/ChatRoom'
import { getChatHistory, resolveWebSocketUrl } from './services/api'
import { ChatConnection } from './services/websocket'
import type { ChatMessage, ConnectionState } from './types'

function mergeMessages(current: ChatMessage[], incoming: ChatMessage[]): ChatMessage[] {
  const byId = new Map<number, ChatMessage>()
  for (const message of current) byId.set(message.id, message)
  for (const message of incoming) byId.set(message.id, message)
  return [...byId.values()].sort((a, b) => a.id - b.id)
}

function App() {
  const [username, setUsername] = useState('')
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [connectionState, setConnectionState] = useState<ConnectionState>('closed')
  const connectionRef = useRef<ChatConnection | null>(null)

  const loadHistory = useCallback(async () => {
    try {
      const history = await getChatHistory()
      setMessages(previous => mergeMessages(previous, history))
    } catch (error) {
      console.error('Failed to load chat history:', error)
    }
  }, [])

  const handleConnect = (inputUsername: string) => {
    connectionRef.current?.stop()
    setMessages([])
    setUsername(inputUsername)

    const connection = new ChatConnection(resolveWebSocketUrl, inputUsername, {
      onMessage: message => setMessages(previous => mergeMessages(previous, [message])),
      onStateChange: setConnectionState,
      onOpen: () => void loadHistory(),
    })
    connectionRef.current = connection
    connection.start()
  }

  const handleSendMessage = (content: string) => {
    if (!connectionRef.current?.send(content)) {
      console.warn('Message not sent: connection is not open')
    }
  }

  const handleDisconnect = () => {
    connectionRef.current?.stop()
    connectionRef.current = null
    setUsername('')
    setMessages([])
  }

  useEffect(() => {
    return () => connectionRef.current?.stop()
  }, [])

  if (!username) {
    return <Login onConnect={handleConnect} />
  }

  return (
    <ChatRoom
      username={username}
      messages={messages}
      connectionState={connectionState}
      onSendMessage={handleSendMessage}
      onDisconnect={handleDisconnect}
    />
  )
}

export default App
