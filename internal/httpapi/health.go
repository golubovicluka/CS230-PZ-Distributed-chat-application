package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"sync/atomic"
	"time"
)

const readinessCheckTimeout = 2 * time.Second

// Check probes one dependency and returns nil when it is usable.
type Check func(context.Context) error

// Liveness reports that the process is running and able to serve HTTP.
func Liveness() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
}

// Readiness reports whether the process should receive new traffic. It fails
// while draining and when any dependency check fails, so orchestrators stop
// routing clients here before shutdown or during an outage.
func Readiness(draining *atomic.Bool, checks map[string]Check) http.Handler {
	names := make([]string, 0, len(checks))
	for name := range checks {
		names = append(names, name)
	}
	sort.Strings(names)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status := http.StatusOK
		results := make(map[string]string, len(checks)+1)

		if draining != nil && draining.Load() {
			status = http.StatusServiceUnavailable
			results["draining"] = "true"
		}

		ctx, cancel := context.WithTimeout(r.Context(), readinessCheckTimeout)
		defer cancel()
		for _, name := range names {
			if err := checks[name](ctx); err != nil {
				status = http.StatusServiceUnavailable
				results[name] = err.Error()
				continue
			}
			results[name] = "ok"
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(results)
	})
}
