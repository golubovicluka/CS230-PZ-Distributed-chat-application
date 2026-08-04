package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCORS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		method         string
		wantStatus     int
		wantNextCalled bool
	}{
		{name: "passes requests to the next handler", method: http.MethodGet, wantStatus: http.StatusNoContent, wantNextCalled: true},
		{name: "handles preflight without calling next", method: http.MethodOptions, wantStatus: http.StatusOK, wantNextCalled: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			nextCalled := false
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				nextCalled = true
				w.WriteHeader(http.StatusNoContent)
			})
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(tt.method, "/", nil)

			CORS(next).ServeHTTP(recorder, request)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, tt.wantStatus)
			}
			if nextCalled != tt.wantNextCalled {
				t.Fatalf("next called = %t, want %t", nextCalled, tt.wantNextCalled)
			}
			if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "*" {
				t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, "*")
			}
			if got := recorder.Header().Get("Access-Control-Allow-Methods"); got != "POST, GET, OPTIONS" {
				t.Errorf("Access-Control-Allow-Methods = %q", got)
			}
			if got := recorder.Header().Get("Access-Control-Allow-Headers"); got != "Content-Type" {
				t.Errorf("Access-Control-Allow-Headers = %q", got)
			}
		})
	}
}
