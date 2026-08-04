package chat

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type stubHistory struct {
	messages []Message
	err      error
	limit    int
}

func (s *stubHistory) Recent(_ context.Context, limit int) ([]Message, error) {
	s.limit = limit
	return s.messages, s.err
}

func TestHistoryHandler(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		method      string
		messages    []Message
		historyErr  error
		wantStatus  int
		wantBody    string
		wantQueried bool
	}{
		{
			name:        "returns messages",
			method:      http.MethodGet,
			messages:    []Message{{ID: 1, Username: "alice", Content: "hello", Server: "ws://one", Timestamp: "2026-08-05 12:00:00"}},
			wantStatus:  http.StatusOK,
			wantBody:    `[{"id":1,"username":"alice","content":"hello","server":"ws://one","timestamp":"2026-08-05 12:00:00"}]` + "\n",
			wantQueried: true,
		},
		{name: "returns an empty array", method: http.MethodGet, wantStatus: http.StatusOK, wantBody: "[]\n", wantQueried: true},
		{name: "returns store error", method: http.MethodGet, historyErr: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError, wantBody: "failed to retrieve message history\n", wantQueried: true},
		{name: "rejects wrong method", method: http.MethodPost, wantStatus: http.StatusMethodNotAllowed, wantBody: "Method Not Allowed\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			history := &stubHistory{messages: tt.messages, err: tt.historyErr}
			handler := NewHandler(nil, history, nil, log.New(io.Discard, "", 0))
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(tt.method, "/history", nil)

			handler.Routes().ServeHTTP(recorder, request)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, tt.wantStatus)
			}
			if recorder.Body.String() != tt.wantBody {
				t.Errorf("body = %q, want %q", recorder.Body.String(), tt.wantBody)
			}
			if got := history.limit != 0; got != tt.wantQueried {
				t.Errorf("history queried = %t, want %t", got, tt.wantQueried)
			}
			if tt.wantQueried && history.limit != historyLimit {
				t.Errorf("history limit = %d, want %d", history.limit, historyLimit)
			}
			if tt.wantStatus == http.StatusOK && recorder.Header().Get("Content-Type") != "application/json" {
				t.Errorf("Content-Type = %q", recorder.Header().Get("Content-Type"))
			}
		})
	}
}

func TestWebSocketUsernameValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		username string
		want     string
		wantErr  bool
	}{
		{name: "accepts username", username: "alice", want: "alice"},
		{name: "trims username", username: "  alice  ", want: "alice"},
		{name: "accepts Unicode at limit", username: strings.Repeat("ž", maxUsernameLength), want: strings.Repeat("ž", maxUsernameLength)},
		{name: "rejects missing username", wantErr: true},
		{name: "rejects blank username", username: "  ", wantErr: true},
		{name: "rejects username above limit", username: strings.Repeat("a", maxUsernameLength+1), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := validUsername(tt.username)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validUsername() error = %v, wantErr %t", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("username = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWebSocketHandlerRejectsInvalidUsername(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		query string
	}{
		{name: "missing"},
		{name: "blank", query: "?username=%20%20"},
		{name: "too long", query: "?username=" + strings.Repeat("a", maxUsernameLength+1)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			handler := NewHandler(nil, nil, nil, log.New(io.Discard, "", 0))
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/ws"+tt.query, nil)

			handler.Routes().ServeHTTP(recorder, request)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
			}
		})
	}
}
