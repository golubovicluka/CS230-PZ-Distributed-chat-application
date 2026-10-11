package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"

	"github.com/golubovicluka/CS230-PZ-Distributed-chat-application/internal/httpapi"
	"github.com/golubovicluka/CS230-PZ-Distributed-chat-application/internal/loadbalancer"
	"github.com/golubovicluka/CS230-PZ-Distributed-chat-application/internal/telemetry"
)

type config struct {
	address           string
	allowedOrigins    string
	serverTTL         time.Duration
	readHeaderTimeout time.Duration
	readTimeout       time.Duration
	writeTimeout      time.Duration
	idleTimeout       time.Duration
	shutdownTimeout   time.Duration
}

func defaultConfig() config {
	return config{
		address:           ":9000",
		allowedOrigins:    "http://localhost:5173,http://127.0.0.1:5173",
		serverTTL:         30 * time.Second,
		readHeaderTimeout: 5 * time.Second,
		readTimeout:       15 * time.Second,
		writeTimeout:      15 * time.Second,
		idleTimeout:       60 * time.Second,
		shutdownTimeout:   10 * time.Second,
	}
}

func main() {
	if err := execute(os.Args[1:], os.Stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		log.Printf("load balancer stopped: %v", err)
		os.Exit(1)
	}
}

func execute(args []string, flagOutput io.Writer) (err error) {
	configuration, err := parseConfig(args, flagOutput)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	instrumentation, err := telemetry.Setup(ctx, "loadbalancer")
	if err != nil {
		return fmt.Errorf("set up telemetry: %w", err)
	}
	defer func() {
		flushContext, cancel := context.WithTimeout(context.Background(), configuration.shutdownTimeout)
		defer cancel()
		if flushErr := instrumentation.Shutdown(flushContext); flushErr != nil {
			err = errors.Join(err, fmt.Errorf("flush telemetry: %w", flushErr))
		}
	}()

	logger := slog.NewLogLogger(instrumentation.Handler, slog.LevelInfo)
	if err := run(ctx, configuration, logger); err != nil {
		return fmt.Errorf("run load balancer: %w", err)
	}
	return nil
}

func parseConfig(args []string, output io.Writer) (config, error) {
	configuration := defaultConfig()
	flags := flag.NewFlagSet("loadbalancer", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&configuration.address, "address", configuration.address, "HTTP listen address")
	flags.StringVar(&configuration.allowedOrigins, "allowed-origins", configuration.allowedOrigins, "comma-separated browser origins allowed by CORS")
	flags.DurationVar(&configuration.serverTTL, "server-ttl", configuration.serverTTL, "time after the last report before a chat server is considered dead")
	flags.DurationVar(&configuration.readHeaderTimeout, "read-header-timeout", configuration.readHeaderTimeout, "maximum time to read request headers")
	flags.DurationVar(&configuration.readTimeout, "read-timeout", configuration.readTimeout, "maximum time to read a request")
	flags.DurationVar(&configuration.writeTimeout, "write-timeout", configuration.writeTimeout, "maximum time to write a response")
	flags.DurationVar(&configuration.idleTimeout, "idle-timeout", configuration.idleTimeout, "maximum keep-alive idle time")
	flags.DurationVar(&configuration.shutdownTimeout, "shutdown-timeout", configuration.shutdownTimeout, "maximum graceful shutdown time")

	if err := flags.Parse(args); err != nil {
		return config{}, fmt.Errorf("parse flags: %w", err)
	}
	if flags.NArg() != 0 {
		return config{}, fmt.Errorf("unexpected positional arguments: %s", strings.Join(flags.Args(), " "))
	}
	if err := configuration.validate(); err != nil {
		return config{}, fmt.Errorf("validate configuration: %w", err)
	}

	return configuration, nil
}

func (configuration config) validate() error {
	if strings.TrimSpace(configuration.address) == "" {
		return errors.New("address must not be empty")
	}

	if _, err := httpapi.ParseOrigins(configuration.allowedOrigins); err != nil {
		return fmt.Errorf("allowed-origins: %w", err)
	}

	timeouts := []struct {
		name  string
		value time.Duration
	}{
		{name: "server-ttl", value: configuration.serverTTL},
		{name: "read-header-timeout", value: configuration.readHeaderTimeout},
		{name: "read-timeout", value: configuration.readTimeout},
		{name: "write-timeout", value: configuration.writeTimeout},
		{name: "idle-timeout", value: configuration.idleTimeout},
		{name: "shutdown-timeout", value: configuration.shutdownTimeout},
	}
	for _, timeout := range timeouts {
		if timeout.value <= 0 {
			return fmt.Errorf("%s must be positive", timeout.name)
		}
	}

	return nil
}

func run(ctx context.Context, configuration config, logger *log.Logger) error {
	if ctx == nil {
		return errors.New("context must not be nil")
	}
	if logger == nil {
		return errors.New("logger must not be nil")
	}
	if err := configuration.validate(); err != nil {
		return fmt.Errorf("validate configuration: %w", err)
	}

	origins, err := httpapi.ParseOrigins(configuration.allowedOrigins)
	if err != nil {
		return fmt.Errorf("parse allowed origins: %w", err)
	}

	balancer := loadbalancer.New(logger, configuration.serverTTL)
	if _, err := otel.Meter("github.com/golubovicluka/CS230-PZ-Distributed-chat-application/cmd/loadbalancer").Int64ObservableGauge(
		"lb.servers.available",
		metric.WithDescription("Chat servers that reported within the TTL."),
		metric.WithUnit("{server}"),
		metric.WithInt64Callback(func(_ context.Context, observer metric.Int64Observer) error {
			observer.Observe(int64(balancer.Available()))
			return nil
		}),
	); err != nil {
		return fmt.Errorf("register available-servers gauge: %w", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/", balancer.Handler())
	mux.Handle("GET /healthz", httpapi.Liveness())
	server := &http.Server{
		Addr:              configuration.address,
		Handler:           otelhttp.NewHandler(httpapi.CORS(origins, mux), "loadbalancer", otelhttp.WithFilter(skipHeartbeats)),
		ReadHeaderTimeout: configuration.readHeaderTimeout,
		ReadTimeout:       configuration.readTimeout,
		WriteTimeout:      configuration.writeTimeout,
		IdleTimeout:       configuration.idleTimeout,
		ErrorLog:          logger,
	}
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(ctx, "tcp", configuration.address)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", configuration.address, err)
	}

	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- server.Serve(listener)
	}()

	logger.Printf("listening on %s", listener.Addr())
	select {
	case err := <-serveErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		if err == nil {
			return errors.New("HTTP server stopped without an error")
		}
		return fmt.Errorf("serve HTTP: %w", err)
	case <-ctx.Done():
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), configuration.shutdownTimeout)
	defer cancelShutdown()
	if err := server.Shutdown(shutdownCtx); err != nil {
		shutdownErr := fmt.Errorf("graceful HTTP shutdown: %w", err)
		if closeErr := server.Close(); closeErr != nil {
			return errors.Join(shutdownErr, fmt.Errorf("force HTTP server close: %w", closeErr))
		}
		return shutdownErr
	}

	if err := <-serveErrors; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve HTTP during shutdown: %w", err)
	}
	logger.Printf("shutdown complete")
	return nil
}

// skipHeartbeats keeps health checks and the periodic load reports out of the
// traces; selections and registrations stay visible.
func skipHeartbeats(r *http.Request) bool {
	return r.URL.Path != "/healthz" && r.URL.Path != "/update"
}
