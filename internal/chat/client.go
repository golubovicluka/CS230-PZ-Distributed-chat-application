package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	maxMessageSize = 512
	clientBuffer   = 256
)

type client struct {
	hub      *Hub
	conn     *websocket.Conn
	send     chan []byte
	username string
}

func newClient(hub *Hub, conn *websocket.Conn, username string) *client {
	return &client{
		hub:      hub,
		conn:     conn,
		send:     make(chan []byte, clientBuffer),
		username: username,
	}
}

func (c *client) readPump(ctx context.Context) {
	defer c.hub.unregisterClient(c)

	c.conn.SetReadLimit(maxMessageSize)
	if err := c.conn.SetReadDeadline(time.Now().Add(pongWait)); err != nil {
		c.hub.logger.Printf("[Server %s] set read deadline for client %q: %v", c.hub.address, c.username, err)
		return
	}
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		_, payload, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure, websocket.CloseNoStatusReceived) {
				c.hub.logger.Printf("[Server %s] client %q read: %v", c.hub.address, c.username, err)
			}
			return
		}

		incoming, err := decodeIncomingMessage(payload)
		if err != nil {
			c.hub.logger.Printf("[Server %s] client %q sent invalid message: %v", c.hub.address, c.username, err)
			continue
		}

		if err := c.hub.handleMessage(ctx, c.username, incoming.Content); err != nil {
			c.hub.logger.Printf("[Server %s] process message from client %q: %v", c.hub.address, c.username, err)
		}
	}
}

func (c *client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()
	defer c.hub.unregisterClient(c)

	for {
		select {
		case message, ok := <-c.send:
			if err := c.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
				c.hub.logger.Printf("[Server %s] set write deadline for client %q: %v", c.hub.address, c.username, err)
				return
			}
			if !ok {
				return
			}

			if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				c.hub.logger.Printf("[Server %s] write to client %q: %v", c.hub.address, c.username, err)
				return
			}

		case <-ticker.C:
			if err := c.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
				c.hub.logger.Printf("[Server %s] set ping deadline for client %q: %v", c.hub.address, c.username, err)
				return
			}
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				c.hub.logger.Printf("[Server %s] ping client %q: %v", c.hub.address, c.username, err)
				return
			}
		}
	}
}

type incomingMessage struct {
	Username string `json:"username"`
	Content  string `json:"content"`
}

func decodeIncomingMessage(payload []byte) (incomingMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()

	var message incomingMessage
	if err := decoder.Decode(&message); err != nil {
		return incomingMessage{}, fmt.Errorf("decode JSON: %w", err)
	}
	if len(bytes.TrimSpace(payload)) == 0 || strings.TrimSpace(message.Content) == "" {
		return incomingMessage{}, errors.New("message content is required")
	}

	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return incomingMessage{}, errors.New("decode JSON: multiple values")
		}
		return incomingMessage{}, fmt.Errorf("decode trailing JSON: %w", err)
	}

	return message, nil
}
