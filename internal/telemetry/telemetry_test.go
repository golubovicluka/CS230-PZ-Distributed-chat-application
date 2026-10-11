package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
)

// isolateTelemetryEnv removes every OTEL_* and PYROSCOPE_* variable for the
// test, restoring them afterwards. Signal-specific variables such as
// OTEL_EXPORTER_OTLP_METRICS_ENDPOINT override the generic endpoint, and CI
// runners like Dagger inject them into every container.
func isolateTelemetryEnv(t *testing.T) {
	t.Helper()
	for _, entry := range os.Environ() {
		key, value, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "OTEL_") || strings.HasPrefix(key, "PYROSCOPE_") {
			t.Setenv(key, value)
			if err := os.Unsetenv(key); err != nil {
				t.Fatalf("unset %s: %v", key, err)
			}
		}
	}
}

func TestSetupWithoutEndpointInstallsNoExporters(t *testing.T) {
	isolateTelemetryEnv(t)

	telemetry, err := Setup(context.Background(), "test-service")
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	if telemetry.Handler == nil {
		t.Fatal("Setup() returned nil log handler")
	}
	if len(telemetry.shutdown) != 0 {
		t.Fatalf("Setup() registered %d shutdown hooks, want none without an endpoint", len(telemetry.shutdown))
	}
	if err := telemetry.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

func TestSetupHonorsSDKDisabled(t *testing.T) {
	isolateTelemetryEnv(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4318")
	t.Setenv("OTEL_SDK_DISABLED", "true")

	telemetry, err := Setup(context.Background(), "test-service")
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	if len(telemetry.shutdown) != 0 {
		t.Fatalf("Setup() registered exporters although OTEL_SDK_DISABLED=true")
	}
}

func TestSetupExportsTracesOverOTLP(t *testing.T) {
	var traceRequests atomic.Int32
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/traces" {
			traceRequests.Add(1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer collector.Close()

	isolateTelemetryEnv(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", collector.URL)

	telemetry, err := Setup(context.Background(), "test-service")
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}

	_, span := otel.Tracer("test").Start(context.Background(), "unit-test-span")
	span.End()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := telemetry.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if traceRequests.Load() == 0 {
		t.Fatal("no trace export reached the collector")
	}
}
