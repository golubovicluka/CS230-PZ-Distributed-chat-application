package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestLiveness(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	Liveness().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
}

func TestReadiness(t *testing.T) {
	t.Parallel()

	healthy := func(context.Context) error { return nil }
	broken := func(context.Context) error { return errors.New("connection refused") }

	tests := []struct {
		name       string
		draining   bool
		checks     map[string]Check
		wantStatus int
		wantBody   map[string]string
	}{
		{name: "ready when every check passes", checks: map[string]Check{"redis": healthy, "postgres": healthy}, wantStatus: http.StatusOK, wantBody: map[string]string{"redis": "ok", "postgres": "ok"}},
		{name: "not ready when a check fails", checks: map[string]Check{"redis": broken, "postgres": healthy}, wantStatus: http.StatusServiceUnavailable, wantBody: map[string]string{"redis": "connection refused", "postgres": "ok"}},
		{name: "not ready while draining", draining: true, checks: map[string]Check{"redis": healthy}, wantStatus: http.StatusServiceUnavailable, wantBody: map[string]string{"draining": "true", "redis": "ok"}},
		{name: "ready without checks", wantStatus: http.StatusOK, wantBody: map[string]string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var draining atomic.Bool
			draining.Store(tt.draining)
			recorder := httptest.NewRecorder()
			Readiness(&draining, tt.checks).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, tt.wantStatus)
			}
			var body map[string]string
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body %q: %v", recorder.Body.String(), err)
			}
			if len(body) != len(tt.wantBody) {
				t.Fatalf("body = %v, want %v", body, tt.wantBody)
			}
			for key, want := range tt.wantBody {
				if body[key] != want {
					t.Errorf("body[%q] = %q, want %q", key, body[key], want)
				}
			}
		})
	}
}
