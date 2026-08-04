package registry

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/iotest"
	"time"
)

func TestNewClientValidation(t *testing.T) {
	tests := []struct {
		name              string
		baseURL           string
		serverAddress     string
		httpClient        *http.Client
		wantErrorContains string
	}{
		{
			name:          "valid configuration",
			baseURL:       "https://registry.example.com",
			serverAddress: "wss://chat.example.com",
			httpClient:    &http.Client{},
		},
		{
			name:          "valid trailing slash",
			baseURL:       "https://registry.example.com/",
			serverAddress: "wss://chat.example.com",
			httpClient:    &http.Client{},
		},
		{
			name:              "empty base URL",
			serverAddress:     "wss://chat.example.com",
			httpClient:        &http.Client{},
			wantErrorContains: "base URL must not be empty",
		},
		{
			name:              "unsupported base URL scheme",
			baseURL:           "ftp://registry.example.com",
			serverAddress:     "wss://chat.example.com",
			httpClient:        &http.Client{},
			wantErrorContains: "unsupported scheme",
		},
		{
			name:              "base URL without host",
			baseURL:           "http:///registry",
			serverAddress:     "wss://chat.example.com",
			httpClient:        &http.Client{},
			wantErrorContains: "must include a host",
		},
		{
			name:              "base URL with path",
			baseURL:           "https://registry.example.com/api",
			serverAddress:     "wss://chat.example.com",
			httpClient:        &http.Client{},
			wantErrorContains: "must not include a path",
		},
		{
			name:              "base URL with query",
			baseURL:           "https://registry.example.com?token=secret",
			serverAddress:     "wss://chat.example.com",
			httpClient:        &http.Client{},
			wantErrorContains: "must not include a query",
		},
		{
			name:              "base URL with fragment",
			baseURL:           "https://registry.example.com#section",
			serverAddress:     "wss://chat.example.com",
			httpClient:        &http.Client{},
			wantErrorContains: "must not include a fragment",
		},
		{
			name:              "base URL with credentials",
			baseURL:           "https://user:password@registry.example.com",
			serverAddress:     "wss://chat.example.com",
			httpClient:        &http.Client{},
			wantErrorContains: "must not include credentials",
		},
		{
			name:              "empty server address",
			baseURL:           "https://registry.example.com",
			httpClient:        &http.Client{},
			wantErrorContains: "server address must not be empty",
		},
		{
			name:              "server address surrounding whitespace",
			baseURL:           "https://registry.example.com",
			serverAddress:     " wss://chat.example.com ",
			httpClient:        &http.Client{},
			wantErrorContains: "server address must not contain surrounding whitespace",
		},
		{
			name:              "nil HTTP client",
			baseURL:           "https://registry.example.com",
			serverAddress:     "wss://chat.example.com",
			wantErrorContains: "HTTP client must not be nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewClient(tt.baseURL, tt.serverAddress, tt.httpClient)
			if tt.wantErrorContains == "" {
				if err != nil {
					t.Fatalf("NewClient() error = %v", err)
				}
				if client == nil {
					t.Fatal("NewClient() client = nil, want concrete client")
				}
				return
			}

			if err == nil {
				t.Fatalf("NewClient() error = nil, want error containing %q", tt.wantErrorContains)
			}
			if client != nil {
				t.Errorf("NewClient() client = %#v, want nil after validation failure", client)
			}
			if !strings.Contains(err.Error(), tt.wantErrorContains) {
				t.Errorf("NewClient() error = %q, want error containing %q", err, tt.wantErrorContains)
			}
		})
	}
}

func TestClientReportsResponseBodyFailure(t *testing.T) {
	readErr := errors.New("read failed")
	closeErr := errors.New("close failed")
	tests := []struct {
		name       string
		statusCode int
		status     string
		body       io.ReadCloser
		want       error
	}{
		{
			name:       "successful response read error",
			statusCode: http.StatusOK,
			status:     "200 OK",
			body:       &testResponseBody{Reader: iotest.ErrReader(readErr)},
			want:       readErr,
		},
		{
			name:       "successful response close error",
			statusCode: http.StatusNoContent,
			status:     "204 No Content",
			body:       &testResponseBody{Reader: strings.NewReader("response"), closeErr: closeErr},
			want:       closeErr,
		},
		{
			name:       "error response read error",
			statusCode: http.StatusBadGateway,
			status:     "502 Bad Gateway",
			body:       &testResponseBody{Reader: iotest.ErrReader(readErr)},
			want:       readErr,
		},
		{
			name:       "error response close error",
			statusCode: http.StatusBadGateway,
			status:     "502 Bad Gateway",
			body:       &testResponseBody{Reader: strings.NewReader("unavailable"), closeErr: closeErr},
			want:       closeErr,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			httpClient := &http.Client{
				Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: tt.statusCode,
						Status:     tt.status,
						Header:     make(http.Header),
						Body:       tt.body,
						Request:    request,
					}, nil
				}),
			}
			client := mustNewClient(t, "https://registry.example.com", httpClient)

			err := client.Register(context.Background())
			if !errors.Is(err, tt.want) {
				t.Fatalf("Register() error = %v, want errors.Is(%v)", err, tt.want)
			}
		})
	}
}

func TestClientDrainsAndClosesSuccessfulResponse(t *testing.T) {
	reader := strings.NewReader("successful response body")
	body := &testResponseBody{Reader: reader}
	httpClient := &http.Client{
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     make(http.Header),
				Body:       body,
				Request:    request,
			}, nil
		}),
	}
	client := mustNewClient(t, "https://registry.example.com", httpClient)

	if err := client.Register(context.Background()); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if remaining := reader.Len(); remaining != 0 {
		t.Errorf("successful response has %d unread bytes, want 0", remaining)
	}
	if !body.closed {
		t.Error("successful response body was not closed")
	}
}

func TestClientPropagatesRequestFailure(t *testing.T) {
	transportErr := errors.New("transport failed")
	tests := []struct {
		name string
		run  func(*testing.T) error
		want error
	}{
		{
			name: "canceled context",
			run: func(t *testing.T) error {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNoContent)
				}))
				t.Cleanup(server.Close)
				client := mustNewClient(t, server.URL, server.Client())
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return client.Register(ctx)
			},
			want: context.Canceled,
		},
		{
			name: "context deadline",
			run: func(t *testing.T) error {
				release := make(chan struct{})
				server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
					<-release
				}))
				t.Cleanup(server.Close)
				defer close(release)
				client := mustNewClient(t, server.URL, server.Client())
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
				defer cancel()
				return client.Register(ctx)
			},
			want: context.DeadlineExceeded,
		},
		{
			name: "HTTP client timeout",
			run: func(t *testing.T) error {
				release := make(chan struct{})
				server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
					<-release
				}))
				t.Cleanup(server.Close)
				defer close(release)
				httpClient := server.Client()
				httpClient.Timeout = 20 * time.Millisecond
				client := mustNewClient(t, server.URL, httpClient)
				return client.UpdateLoad(context.Background(), 1)
			},
			want: context.DeadlineExceeded,
		},
		{
			name: "transport error",
			run: func(t *testing.T) error {
				httpClient := &http.Client{
					Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
						return nil, transportErr
					}),
				}
				client := mustNewClient(t, "https://registry.example.com", httpClient)
				return client.UpdateLoad(context.Background(), 1)
			},
			want: transportErr,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run(t)
			if !errors.Is(err, tt.want) {
				t.Fatalf("registry operation error = %v, want errors.Is(%v)", err, tt.want)
			}
		})
	}
}

func TestClientUpdateLoadRejectsNegativeLoad(t *testing.T) {
	tests := []struct {
		name string
		load int
	}{
		{name: "minus one", load: -1},
		{name: "large negative", load: -100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requestCount atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requestCount.Add(1)
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()

			client, err := NewClient(server.URL, "ws://chat.example.com", server.Client())
			if err != nil {
				t.Fatalf("NewClient() error = %v", err)
			}

			err = client.UpdateLoad(context.Background(), tt.load)
			if err == nil {
				t.Fatal("UpdateLoad() error = nil, want negative-load error")
			}
			if !strings.Contains(err.Error(), "load must not be negative") {
				t.Errorf("UpdateLoad() error = %q, want negative-load context", err)
			}
			if got := requestCount.Load(); got != 0 {
				t.Errorf("registry request count = %d, want 0", got)
			}
		})
	}
}

func TestClientSendsExactRequest(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		wantBody string
		call     func(context.Context, *Client) error
	}{
		{
			name:     "register",
			path:     "/register",
			wantBody: `{"address":"ws://chat.example.com","load":0}`,
			call: func(ctx context.Context, client *Client) error {
				return client.Register(ctx)
			},
		},
		{
			name:     "update",
			path:     "/update",
			wantBody: `{"address":"ws://chat.example.com","load":7}`,
			call: func(ctx context.Context, client *Client) error {
				return client.UpdateLoad(ctx, 7)
			},
		},
	}

	type request struct {
		method      string
		path        string
		contentType string
		body        string
		err         error
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requests := make(chan request, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				requests <- request{
					method:      r.Method,
					path:        r.URL.Path,
					contentType: r.Header.Get("Content-Type"),
					body:        string(body),
					err:         err,
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()

			client, err := NewClient(server.URL+"/", "ws://chat.example.com", server.Client())
			if err != nil {
				t.Fatalf("NewClient() error = %v", err)
			}

			if err := tt.call(context.Background(), client); err != nil {
				t.Fatalf("request error = %v", err)
			}

			got := <-requests
			if got.err != nil {
				t.Fatalf("read request body: %v", got.err)
			}
			if got.method != http.MethodPost {
				t.Errorf("request method = %q, want %q", got.method, http.MethodPost)
			}
			if got.path != tt.path {
				t.Errorf("request path = %q, want %q", got.path, tt.path)
			}
			if got.contentType != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", got.contentType)
			}
			if got.body != tt.wantBody {
				t.Errorf("request body = %q, want %q", got.body, tt.wantBody)
			}
		})
	}
}

func TestClientReportsErrorStatus(t *testing.T) {
	const errorBodyLimit = 4 << 10

	tests := []struct {
		name             string
		status           int
		body             string
		wantBodyContains string
		wantBodyAbsent   string
		call             func(context.Context, *Client) error
	}{
		{
			name:             "register bad request",
			status:           http.StatusBadRequest,
			body:             "invalid registration",
			wantBodyContains: "invalid registration",
			call: func(ctx context.Context, client *Client) error {
				return client.Register(ctx)
			},
		},
		{
			name:           "update bounds response body",
			status:         http.StatusServiceUnavailable,
			body:           strings.Repeat("x", errorBodyLimit) + "UNBOUNDED_SENTINEL",
			wantBodyAbsent: "UNBOUNDED_SENTINEL",
			call: func(ctx context.Context, client *Client) error {
				return client.UpdateLoad(ctx, 3)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				if _, err := io.WriteString(w, tt.body); err != nil {
					t.Errorf("write response body: %v", err)
				}
			}))
			defer server.Close()

			client, err := NewClient(server.URL, "ws://chat.example.com", server.Client())
			if err != nil {
				t.Fatalf("NewClient() error = %v", err)
			}

			err = tt.call(context.Background(), client)
			if err == nil {
				t.Fatal("registry operation error = nil, want HTTP status error")
			}
			if !strings.Contains(err.Error(), http.StatusText(tt.status)) {
				t.Errorf("registry operation error = %q, want status %q", err, http.StatusText(tt.status))
			}
			if tt.wantBodyContains != "" && !strings.Contains(err.Error(), tt.wantBodyContains) {
				t.Errorf("registry operation error = %q, want body detail %q", err, tt.wantBodyContains)
			}
			if tt.wantBodyAbsent != "" && strings.Contains(err.Error(), tt.wantBodyAbsent) {
				t.Errorf("registry operation error contains body beyond %d bytes", errorBodyLimit)
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

type testResponseBody struct {
	io.Reader
	closeErr error
	closed   bool
}

func (body *testResponseBody) Close() error {
	body.closed = true
	return body.closeErr
}

func mustNewClient(t *testing.T, baseURL string, httpClient *http.Client) *Client {
	t.Helper()

	client, err := NewClient(baseURL, "ws://chat.example.com", httpClient)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return client
}
