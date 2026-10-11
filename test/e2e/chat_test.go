//go:build e2e

package e2e

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type message struct {
	Username string `json:"username"`
	Content  string `json:"content"`
	Server   string `json:"server"`
}

type target struct {
	base      *url.URL
	tlsConfig *tls.Config
}

func newTarget(t *testing.T) target {
	t.Helper()
	raw := os.Getenv("E2E_BASE_URL")
	if raw == "" {
		raw = "https://chat.localhost"
	}
	base, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("E2E_BASE_URL: %v", err)
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if caFile := os.Getenv("E2E_CA_FILE"); caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			t.Fatalf("read E2E_CA_FILE: %v", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			t.Fatalf("no certificates in %s", caFile)
		}
		tlsConfig.RootCAs = pool
	}
	return target{base: base, tlsConfig: tlsConfig}
}

func dialLoopback(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		address = net.JoinHostPort("127.0.0.1", port)
	}
	var dialer net.Dialer
	return dialer.DialContext(ctx, network, address)
}

func (tg target) httpClient() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext:     dialLoopback,
			TLSClientConfig: tg.tlsConfig,
		},
	}
}

func (tg target) dial(username string) (*websocket.Conn, error) {
	wsURL := *tg.base
	wsURL.Scheme = map[string]string{"https": "wss", "http": "ws"}[tg.base.Scheme]
	wsURL.Path = "/ws"
	wsURL.RawQuery = url.Values{"username": {username}}.Encode()

	dialer := websocket.Dialer{
		NetDialContext:   dialLoopback,
		TLSClientConfig:  tg.tlsConfig,
		HandshakeTimeout: 10 * time.Second,
	}
	header := http.Header{"Origin": {tg.base.Scheme + "://" + tg.base.Host}}
	conn, response, err := dialer.Dial(wsURL.String(), header)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	return conn, err
}

func (tg target) connect(t *testing.T, username string) *websocket.Conn {
	t.Helper()
	conn, err := tg.dial(username)
	if err != nil {
		t.Fatalf("connect %s: %v", username, err)
	}
	return conn
}

func awaitContent(conn *websocket.Conn, content string, timeout time.Duration) (message, error) {
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return message{}, err
	}
	for {
		var received message
		if err := conn.ReadJSON(&received); err != nil {
			return message{}, err
		}
		if received.Content == content {
			return received, nil
		}
	}
}

func TestChatAcrossConnections(t *testing.T) {
	tg := newTarget(t)
	content := fmt.Sprintf("e2e %d", time.Now().UnixNano())

	alice := tg.connect(t, "alice-e2e")
	defer alice.Close()
	bob := tg.connect(t, "bob-e2e")
	defer bob.Close()
	time.Sleep(time.Second)

	if err := alice.WriteJSON(map[string]string{"content": content}); err != nil {
		t.Fatalf("send: %v", err)
	}
	for name, conn := range map[string]*websocket.Conn{"alice": alice, "bob": bob} {
		received, err := awaitContent(conn, content, 10*time.Second)
		if err != nil {
			t.Fatalf("%s did not receive the message: %v", name, err)
		}
		t.Logf("%s received %q from %s via server %s", name, received.Content, received.Username, received.Server)
	}

	response, err := tg.httpClient().Get(tg.base.JoinPath("/api/history").String())
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("history status = %d", response.StatusCode)
	}
	var history []message
	if err := json.NewDecoder(response.Body).Decode(&history); err != nil {
		t.Fatalf("decode history: %v", err)
	}
	for _, entry := range history {
		if entry.Content == content {
			return
		}
	}
	t.Fatalf("history does not contain %q", content)
}

func TestGracefulDrain(t *testing.T) {
	restart := os.Getenv("E2E_RESTART_CMD")
	if restart == "" {
		t.Skip("set E2E_RESTART_CMD to a command that restarts the chat servers")
	}
	tg := newTarget(t)
	conn := tg.connect(t, "drain-e2e")
	defer conn.Close()

	output, err := exec.Command("sh", "-c", restart).CombinedOutput()
	if err != nil {
		t.Fatalf("restart: %v\n%s", err, output)
	}

	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	for {
		_, _, err = conn.ReadMessage()
		if err != nil {
			break
		}
	}
	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) || closeErr.Code != websocket.CloseGoingAway {
		t.Fatalf("connection ended with %v, want close code %d (going away)", err, websocket.CloseGoingAway)
	}

	deadline := time.Now().Add(time.Minute)
	for {
		reconnected, dialErr := tg.dial("drain-e2e")
		if dialErr == nil {
			_ = reconnected.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("reconnect after drain: %v", dialErr)
		}
		time.Sleep(2 * time.Second)
	}
}
