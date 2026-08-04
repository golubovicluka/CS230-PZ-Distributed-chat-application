package chat

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type saverFunc func(context.Context, Message) (Message, error)

func (f saverFunc) Save(ctx context.Context, message Message) (Message, error) {
	return f(ctx, message)
}

type publisherFunc func(context.Context, Message) error

func (f publisherFunc) Publish(ctx context.Context, message Message) error {
	return f(ctx, message)
}

type reporterFunc func(context.Context, int) error

func (f reporterFunc) UpdateLoad(ctx context.Context, load int) error {
	return f(ctx, load)
}

func TestHubHandleMessage(t *testing.T) {
	t.Parallel()

	saveFailure := errors.New("save failure")
	publishFailure := errors.New("publish failure")
	tests := []struct {
		name            string
		saveErr         error
		publishErr      error
		wantErrContains string
		wantPublished   bool
	}{
		{name: "saves then publishes", wantPublished: true},
		{name: "stops after save failure", saveErr: saveFailure, wantErrContains: "save message"},
		{name: "returns publish failure", publishErr: publishFailure, wantErrContains: "publish message", wantPublished: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var savedInput Message
			stored := Message{ID: 42, Username: "alice", Content: "hello", Server: "ws://server", Timestamp: "2026-08-05 12:00:00"}
			saver := saverFunc(func(_ context.Context, message Message) (Message, error) {
				savedInput = message
				if tt.saveErr != nil {
					return Message{}, tt.saveErr
				}
				return stored, nil
			})
			var published Message
			publisher := publisherFunc(func(_ context.Context, message Message) error {
				published = message
				return tt.publishErr
			})
			hub := NewHub("ws://server", saver, publisher, reporterFunc(func(context.Context, int) error { return nil }), log.New(io.Discard, "", 0))

			err := hub.handleMessage(context.Background(), "alice", "hello")
			if tt.wantErrContains == "" && err != nil {
				t.Fatalf("handleMessage() error = %v", err)
			}
			if tt.wantErrContains != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErrContains)) {
				t.Fatalf("handleMessage() error = %v, want containing %q", err, tt.wantErrContains)
			}
			if savedInput.Username != "alice" || savedInput.Content != "hello" || savedInput.Server != "ws://server" {
				t.Errorf("saved message = %+v", savedInput)
			}
			if tt.wantPublished && published != stored {
				t.Errorf("published message = %+v, want %+v", published, stored)
			}
			if !tt.wantPublished && published != (Message{}) {
				t.Errorf("published message = %+v, want zero value", published)
			}
		})
	}
}

type blockingReporter struct {
	firstCall sync.Once
	started   chan int
	release   chan struct{}
	calls     chan int
}

func (r *blockingReporter) UpdateLoad(ctx context.Context, load int) error {
	r.calls <- load
	blocked := false
	r.firstCall.Do(func() {
		blocked = true
		r.started <- load
	})
	if !blocked {
		return nil
	}

	select {
	case <-r.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestHubLoadReportingDoesNotBlockClientEvents(t *testing.T) {
	serverConnectionOne, closePairOne := newWebSocketPair(t)
	defer closePairOne()
	serverConnectionTwo, closePairTwo := newWebSocketPair(t)
	defer closePairTwo()

	reporter := &blockingReporter{
		started: make(chan int, 1),
		release: make(chan struct{}),
		calls:   make(chan int, 8),
	}
	hub := newTestHub(reporter)
	broadcasts := make(chan []byte, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErrors := make(chan error, 1)
	go func() { runErrors <- hub.Run(ctx, broadcasts) }()

	clientOne := &client{hub: hub, conn: serverConnectionOne, send: make(chan []byte, 1), username: "one"}
	clientTwo := &client{hub: hub, conn: serverConnectionTwo, send: make(chan []byte, 1), username: "two"}
	registerContext, stopRegister := context.WithTimeout(context.Background(), time.Second)
	defer stopRegister()
	if err := hub.registerClient(registerContext, clientOne); err != nil {
		t.Fatalf("register first client: %v", err)
	}
	select {
	case load := <-reporter.started:
		if load != 1 {
			t.Fatalf("first reported load = %d, want 1", load)
		}
	case <-time.After(time.Second):
		t.Fatal("load reporter did not start")
	}

	if err := hub.registerClient(registerContext, clientTwo); err != nil {
		t.Fatalf("register second client while reporter is blocked: %v", err)
	}
	broadcasts <- []byte("message")
	for name, outbound := range map[string]<-chan []byte{"one": clientOne.send, "two": clientTwo.send} {
		select {
		case payload := <-outbound:
			if string(payload) != "message" {
				t.Errorf("client %s payload = %q", name, payload)
			}
		case <-time.After(time.Second):
			t.Fatalf("client %s did not receive broadcast while reporter was blocked", name)
		}
	}

	hub.unregisterClient(clientOne)
	hub.unregisterClient(clientTwo)
	close(reporter.release)

	deadline := time.After(time.Second)
	for {
		select {
		case load := <-reporter.calls:
			if load == 0 {
				cancel()
				if err := <-runErrors; err != nil {
					t.Fatalf("Run() error = %v", err)
				}
				return
			}
		case <-deadline:
			t.Fatal("latest load 0 was not reported")
		}
	}
}

func TestHubRemovesSlowClient(t *testing.T) {
	serverConnection, closePair := newWebSocketPair(t)
	defer closePair()

	loads := make(chan int, 4)
	hub := newTestHub(reporterFunc(func(_ context.Context, load int) error {
		loads <- load
		return nil
	}))
	broadcasts := make(chan []byte, 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErrors := make(chan error, 1)
	go func() { runErrors <- hub.Run(ctx, broadcasts) }()

	slowClient := &client{hub: hub, conn: serverConnection, send: make(chan []byte, 1), username: "slow"}
	registerContext, stopRegister := context.WithTimeout(context.Background(), time.Second)
	defer stopRegister()
	if err := hub.registerClient(registerContext, slowClient); err != nil {
		t.Fatalf("register client: %v", err)
	}
	waitForLoad(t, loads, 1)

	broadcasts <- []byte("first")
	broadcasts <- []byte("second")
	waitForLoad(t, loads, 0)

	if payload := <-slowClient.send; string(payload) != "first" {
		t.Errorf("buffered payload = %q, want first", payload)
	}
	if _, ok := <-slowClient.send; ok {
		t.Fatal("slow client send channel remains open")
	}

	cancel()
	if err := <-runErrors; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestHubStopsWhenBroadcastSubscriptionCloses(t *testing.T) {
	t.Parallel()

	hub := newTestHub(reporterFunc(func(context.Context, int) error { return nil }))
	broadcasts := make(chan []byte)
	close(broadcasts)

	err := hub.Run(context.Background(), broadcasts)
	if err == nil || !strings.Contains(err.Error(), "subscription closed") {
		t.Fatalf("Run() error = %v, want subscription closed", err)
	}

	registerContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := hub.registerClient(registerContext, &client{}); !errors.Is(err, ErrHubStopped) {
		t.Fatalf("register after stop error = %v, want %v", err, ErrHubStopped)
	}
}

func TestHubTreatsSubscriptionClosureDuringCancellationAsNormal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		closeSubscription bool
	}{
		{name: "context canceled with open subscription"},
		{name: "context canceled with closed subscription", closeSubscription: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			broadcasts := make(chan []byte)
			if tt.closeSubscription {
				close(broadcasts)
			}

			hub := newTestHub(reporterFunc(func(context.Context, int) error { return nil }))
			if err := hub.Run(ctx, broadcasts); err != nil {
				t.Fatalf("Run() error = %v, want normal cancellation", err)
			}
		})
	}
}

func newTestHub(reporter LoadReporter) *Hub {
	saver := saverFunc(func(_ context.Context, message Message) (Message, error) { return message, nil })
	publisher := publisherFunc(func(context.Context, Message) error { return nil })
	return NewHub("ws://test", saver, publisher, reporter, log.New(io.Discard, "", 0))
}

func waitForLoad(t *testing.T, loads <-chan int, want int) {
	t.Helper()
	select {
	case got := <-loads:
		if got != want {
			t.Fatalf("reported load = %d, want %d", got, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for load %d", want)
	}
}

func newWebSocketPair(t *testing.T) (*websocket.Conn, func()) {
	t.Helper()

	serverConnections := make(chan *websocket.Conn, 1)
	upgradeErrors := make(chan error, 1)
	releaseHandler := make(chan struct{})
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			upgradeErrors <- err
			return
		}
		serverConnections <- connection
		<-releaseHandler
	}))

	webSocketURL := "ws" + strings.TrimPrefix(server.URL, "http")
	clientConnection, response, err := websocket.DefaultDialer.Dial(webSocketURL, nil)
	if err != nil && response != nil && response.Body != nil {
		if closeErr := response.Body.Close(); closeErr != nil {
			t.Logf("close WebSocket handshake response: %v", closeErr)
		}
	}
	if err != nil {
		close(releaseHandler)
		server.Close()
		t.Fatalf("dial test WebSocket: %v", err)
	}

	var serverConnection *websocket.Conn
	select {
	case serverConnection = <-serverConnections:
	case err := <-upgradeErrors:
		if closeErr := clientConnection.Close(); closeErr != nil {
			t.Logf("close client WebSocket: %v", closeErr)
		}
		close(releaseHandler)
		server.Close()
		t.Fatalf("upgrade test WebSocket: %v", err)
	case <-time.After(time.Second):
		if closeErr := clientConnection.Close(); closeErr != nil {
			t.Logf("close client WebSocket: %v", closeErr)
		}
		close(releaseHandler)
		server.Close()
		t.Fatal("timed out waiting for server WebSocket")
	}

	var closeOnce sync.Once
	cleanup := func() {
		closeOnce.Do(func() {
			if err := clientConnection.Close(); err != nil {
				t.Logf("close client WebSocket: %v", err)
			}
			if err := serverConnection.Close(); err != nil {
				t.Logf("close server WebSocket: %v", err)
			}
			close(releaseHandler)
			server.Close()
		})
	}
	return serverConnection, cleanup
}
