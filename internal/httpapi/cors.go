package httpapi

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// OriginPolicy decides which browser origins may call the HTTP API and open
// WebSocket connections. Requests without an Origin header come from
// non-browser clients and are always allowed; same-origin requests are
// allowed so the policy works unchanged behind a reverse proxy.
type OriginPolicy struct {
	allowAll bool
	origins  map[string]struct{}
}

// ParseOrigins builds a policy from a comma-separated list of origins such as
// "http://localhost:5173,https://chat.example.com". A single "*" allows every
// origin and is intended for local experiments only.
func ParseOrigins(value string) (OriginPolicy, error) {
	policy := OriginPolicy{origins: make(map[string]struct{})}
	for _, item := range strings.Split(value, ",") {
		origin := strings.TrimSpace(item)
		if origin == "" {
			continue
		}
		if origin == "*" {
			policy.allowAll = true
			continue
		}

		parsed, err := url.Parse(origin)
		if err != nil {
			return OriginPolicy{}, fmt.Errorf("origin %q: %w", origin, err)
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return OriginPolicy{}, fmt.Errorf("origin %q must use http or https", origin)
		}
		if parsed.Host == "" || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
			return OriginPolicy{}, fmt.Errorf("origin %q must be scheme://host[:port]", origin)
		}
		policy.origins[parsed.Scheme+"://"+strings.ToLower(parsed.Host)] = struct{}{}
	}
	return policy, nil
}

// Allows reports whether the request's Origin is acceptable.
func (p OriginPolicy) Allows(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" || p.allowAll {
		return true
	}

	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	if strings.EqualFold(parsed.Host, r.Host) {
		return true
	}
	_, ok := p.origins[parsed.Scheme+"://"+strings.ToLower(parsed.Host)]
	return ok
}

// CORS applies the public HTTP policy shared by both backend processes.
// Disallowed origins receive no CORS headers, so browsers block the response.
func CORS(policy OriginPolicy, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Origin")
		if origin := r.Header.Get("Origin"); origin != "" && policy.Allows(r) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
