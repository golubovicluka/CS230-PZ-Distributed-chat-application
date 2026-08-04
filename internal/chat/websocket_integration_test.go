package chat

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestWebSocketMessageFlow(t *testing.T) {
	broadcasts := make(chan []byte, 1)
	published := make(chan Message, 1)
	loads := make(chan int, 4)
	saver := saverFunc(func(_ context.Context, message Message) (Message, error) {
		message.ID = 7
		message.Timestamp = "2026-08-05T12:00:00Z"
		return message, nil
	})
	publisher := publisherFunc(func(_ context.Context, message Message) error {
		payload, err := json.Marshal(message)
		if err != nil {
			return err
		}
		published <- message
		broadcasts <- payload
		return nil
	})
	reporter := reporterFunc(func(_ context.Context, load int) error {
		loads <- load
		return nil
	})
	logger := log.New(io.Discard, "", 0)
	hub := NewHub("ws://test", saver, publisher, reporter, logger)
	history := &stubHistory{}
	handler := NewHandler(hub, history, func(*http.Request) bool { return true }, logger)

	ctx, cancel := context.WithCancel(context.Background())
	runErrors := make(chan error, 1)
	go func() { runErrors <- hub.Run(ctx, broadcasts) }()

	server := httptest.NewServer(handler.Routes())
	webSocketURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?username=alice"
	connection, response, err := websocket.DefaultDialer.Dial(webSocketURL, nil)
	if err != nil {
		if response != nil && response.Body != nil {
			if closeErr := response.Body.Close(); closeErr != nil {
				t.Logf("close failed handshake response: %v", closeErr)
			}
		}
		cancel()
		server.Close()
		t.Fatalf("dial chat WebSocket: %v", err)
	}
	defer func() {
		cancel()
		if closeErr := connection.Close(); closeErr != nil {
			t.Logf("close chat WebSocket: %v", closeErr)
		}
		server.Close()
		if runErr := <-runErrors; runErr != nil {
			t.Errorf("Run() error = %v", runErr)
		}
	}()

	waitForLoad(t, loads, 1)
	if err := connection.WriteJSON(map[string]string{"username": "mallory", "content": "hello"}); err != nil {
		t.Fatalf("write chat message: %v", err)
	}

	select {
	case message := <-published:
		if message.Username != "alice" {
			t.Errorf("published username = %q, want authenticated username %q", message.Username, "alice")
		}
		if message.Content != "hello" || message.ID != 7 || message.Timestamp == "" {
			t.Errorf("published message = %+v", message)
		}
	case <-time.After(time.Second):
		t.Fatal("message was not published")
	}

	if err := connection.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set client read deadline: %v", err)
	}
	_, payload, err := connection.ReadMessage()
	if err != nil {
		t.Fatalf("read broadcast message: %v", err)
	}
	var received Message
	if err := json.Unmarshal(payload, &received); err != nil {
		t.Fatalf("decode broadcast message: %v", err)
	}
	if received.Username != "alice" || received.Content != "hello" || received.ID != 7 {
		t.Errorf("received message = %+v", received)
	}
}
