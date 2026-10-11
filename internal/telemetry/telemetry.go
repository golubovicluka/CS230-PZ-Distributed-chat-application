// Package telemetry wires OpenTelemetry traces, metrics and logs plus
// continuous profiling for both backend processes.
//
// Everything is opt-in through the standard environment variables, so a
// plain `go run` stays quiet:
//   - OTEL_EXPORTER_OTLP_ENDPOINT enables OTLP/HTTP export of all three
//     signals (OTEL_SERVICE_NAME and OTEL_RESOURCE_ATTRIBUTES are honored);
//   - PYROSCOPE_SERVER_ADDRESS enables push-mode profiling.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/grafana/pyroscope-go"
	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Telemetry owns the installed providers and the log handler that the
// process should use.
type Telemetry struct {
	// Handler writes JSON logs to stdout and, when export is enabled, also
	// ships each record over OTLP with its trace context.
	Handler  slog.Handler
	shutdown []func(context.Context) error
}

// Setup installs global providers for service. It always returns a usable
// Telemetry; exporters are only created when the environment asks for them.
func Setup(ctx context.Context, service string) (*Telemetry, error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	stdout := slog.NewJSONHandler(os.Stdout, nil).WithAttrs([]slog.Attr{slog.String("service", service)})
	t := &Telemetry{Handler: stdout}

	if otlpEnabled() {
		if err := t.startOTLP(ctx, service, stdout); err != nil {
			return nil, errors.Join(err, t.Shutdown(ctx))
		}
	}
	if address := os.Getenv("PYROSCOPE_SERVER_ADDRESS"); address != "" {
		if err := t.startProfiling(service, address); err != nil {
			return nil, errors.Join(err, t.Shutdown(ctx))
		}
	}
	return t, nil
}

// Shutdown flushes and stops every provider in reverse start order.
func (t *Telemetry) Shutdown(ctx context.Context) error {
	var err error
	for i := len(t.shutdown) - 1; i >= 0; i-- {
		err = errors.Join(err, t.shutdown[i](ctx))
	}
	t.shutdown = nil
	return err
}

func otlpEnabled() bool {
	if strings.EqualFold(os.Getenv("OTEL_SDK_DISABLED"), "true") {
		return false
	}
	return os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != ""
}

func (t *Telemetry) startOTLP(ctx context.Context, service string, stdout slog.Handler) error {
	res, err := resource.New(ctx,
		resource.WithTelemetrySDK(),
		resource.WithHost(),
		resource.WithAttributes(attribute.String("service.name", service)),
		resource.WithFromEnv(),
	)
	if err != nil {
		return fmt.Errorf("build telemetry resource: %w", err)
	}

	traceExporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return fmt.Errorf("create trace exporter: %w", err)
	}
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithBatcher(traceExporter), sdktrace.WithResource(res))
	otel.SetTracerProvider(tracerProvider)
	t.shutdown = append(t.shutdown, tracerProvider.Shutdown)

	metricExporter, err := otlpmetrichttp.New(ctx)
	if err != nil {
		return fmt.Errorf("create metric exporter: %w", err)
	}
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter)), sdkmetric.WithResource(res))
	otel.SetMeterProvider(meterProvider)
	t.shutdown = append(t.shutdown, meterProvider.Shutdown)
	if err := runtime.Start(runtime.WithMeterProvider(meterProvider)); err != nil {
		return fmt.Errorf("start runtime metrics: %w", err)
	}

	logExporter, err := otlploghttp.New(ctx)
	if err != nil {
		return fmt.Errorf("create log exporter: %w", err)
	}
	loggerProvider := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewBatchProcessor(logExporter)), sdklog.WithResource(res))
	otel.SetLoggerProvider(loggerProvider)
	t.shutdown = append(t.shutdown, loggerProvider.Shutdown)

	t.Handler = slog.NewMultiHandler(stdout, otelslog.NewHandler(service, otelslog.WithLoggerProvider(loggerProvider)))
	return nil
}

func (t *Telemetry) startProfiling(service, address string) error {
	name := os.Getenv("PYROSCOPE_APPLICATION_NAME")
	if name == "" {
		name = service
	}
	hostname, _ := os.Hostname()

	profiler, err := pyroscope.Start(pyroscope.Config{
		ApplicationName: name,
		ServerAddress:   address,
		Tags:            map[string]string{"hostname": hostname},
		ProfileTypes: []pyroscope.ProfileType{
			pyroscope.ProfileCPU,
			pyroscope.ProfileAllocObjects,
			pyroscope.ProfileAllocSpace,
			pyroscope.ProfileInuseObjects,
			pyroscope.ProfileInuseSpace,
			pyroscope.ProfileGoroutines,
		},
	})
	if err != nil {
		return fmt.Errorf("start pyroscope profiler: %w", err)
	}
	t.shutdown = append(t.shutdown, func(context.Context) error { return profiler.Stop() })
	return nil
}
