package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseOrigins(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "empty list", value: ""},
		{name: "single origin", value: "http://localhost:5173"},
		{name: "several origins with spaces", value: "http://localhost:5173, https://chat.example.com"},
		{name: "wildcard", value: "*"},
		{name: "rejects missing scheme", value: "localhost:5173", wantErr: true},
		{name: "rejects WebSocket scheme", value: "ws://localhost:8080", wantErr: true},
		{name: "rejects path", value: "http://localhost:5173/app", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := ParseOrigins(tt.value); (err != nil) != tt.wantErr {
				t.Fatalf("ParseOrigins(%q) error = %v, wantErr %t", tt.value, err, tt.wantErr)
			}
		})
	}
}

func TestOriginPolicyAllows(t *testing.T) {
	t.Parallel()

	policy, err := ParseOrigins("http://localhost:5173")
	if err != nil {
		t.Fatalf("ParseOrigins() error = %v", err)
	}
	wildcard, err := ParseOrigins("*")
	if err != nil {
		t.Fatalf("ParseOrigins(*) error = %v", err)
	}

	tests := []struct {
		name   string
		policy OriginPolicy
		host   string
		origin string
		want   bool
	}{
		{name: "no origin header", policy: policy, host: "chat:8080", want: true},
		{name: "listed origin", policy: policy, host: "127.0.0.1:8081", origin: "http://localhost:5173", want: true},
		{name: "listed origin is case insensitive", policy: policy, host: "127.0.0.1:8081", origin: "http://LOCALHOST:5173", want: true},
		{name: "same origin behind proxy", policy: policy, host: "localhost:8080", origin: "http://localhost:8080", want: true},
		{name: "unlisted origin", policy: policy, host: "127.0.0.1:8081", origin: "https://evil.example", want: false},
		{name: "scheme must match", policy: policy, host: "127.0.0.1:8081", origin: "https://localhost:5173", want: false},
		{name: "malformed origin", policy: policy, host: "127.0.0.1:8081", origin: "::", want: false},
		{name: "wildcard allows anything", policy: wildcard, host: "127.0.0.1:8081", origin: "https://evil.example", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.Host = tt.host
			if tt.origin != "" {
				request.Header.Set("Origin", tt.origin)
			}
			if got := tt.policy.Allows(request); got != tt.want {
				t.Fatalf("Allows() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestCORS(t *testing.T) {
	t.Parallel()

	policy, err := ParseOrigins("http://localhost:5173")
	if err != nil {
		t.Fatalf("ParseOrigins() error = %v", err)
	}

	tests := []struct {
		name           string
		method         string
		origin         string
		wantStatus     int
		wantNextCalled bool
		wantAllow      string
	}{
		{name: "passes allowed requests to the next handler", method: http.MethodGet, origin: "http://localhost:5173", wantStatus: http.StatusTeapot, wantNextCalled: true, wantAllow: "http://localhost:5173"},
		{name: "handles preflight without calling next", method: http.MethodOptions, origin: "http://localhost:5173", wantStatus: http.StatusNoContent, wantAllow: "http://localhost:5173"},
		{name: "omits headers for unlisted origins", method: http.MethodGet, origin: "https://evil.example", wantStatus: http.StatusTeapot, wantNextCalled: true},
		{name: "omits headers without an origin", method: http.MethodGet, wantStatus: http.StatusTeapot, wantNextCalled: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			nextCalled := false
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				nextCalled = true
				w.WriteHeader(http.StatusTeapot)
			})
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(tt.method, "/", nil)
			request.Host = "127.0.0.1:8081"
			if tt.origin != "" {
				request.Header.Set("Origin", tt.origin)
			}

			CORS(policy, next).ServeHTTP(recorder, request)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, tt.wantStatus)
			}
			if nextCalled != tt.wantNextCalled {
				t.Fatalf("next called = %t, want %t", nextCalled, tt.wantNextCalled)
			}
			if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != tt.wantAllow {
				t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, tt.wantAllow)
			}
			if got := recorder.Header().Get("Vary"); got != "Origin" {
				t.Errorf("Vary = %q, want Origin", got)
			}
		})
	}
}
