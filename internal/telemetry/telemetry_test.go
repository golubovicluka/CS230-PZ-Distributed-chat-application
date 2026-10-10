package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
)

func TestSetupWithoutEndpointInstallsNoExporters(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("PYROSCOPE_SERVER_ADDRESS", "")

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
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4318")
	t.Setenv("OTEL_SDK_DISABLED", "true")
	t.Setenv("PYROSCOPE_SERVER_ADDRESS", "")

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

	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", collector.URL)
	t.Setenv("OTEL_SDK_DISABLED", "")
	t.Setenv("PYROSCOPE_SERVER_ADDRESS", "")

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
