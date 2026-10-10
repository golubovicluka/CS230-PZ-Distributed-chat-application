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
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/extra/redisotel/v9"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/golubovicluka/CS230-PZ-Distributed-chat-application/internal/chat"
	"github.com/golubovicluka/CS230-PZ-Distributed-chat-application/internal/httpapi"
	"github.com/golubovicluka/CS230-PZ-Distributed-chat-application/internal/postgres"
	"github.com/golubovicluka/CS230-PZ-Distributed-chat-application/internal/registry"
	"github.com/golubovicluka/CS230-PZ-Distributed-chat-application/internal/telemetry"
)

const (
	registryRequestTimeout = 3 * time.Second
	deregisterTimeout      = 2 * time.Second
	shutdownTimeout        = 5 * time.Second
	telemetryFlushTimeout  = 5 * time.Second
)

type config struct {
	name              string
	listenAddress     string
	advertisedURL     string
	databaseURL       string
	redisAddress      string
	redisChannel      string
	registryBaseURL   string
	heartbeatInterval time.Duration
	allowedOrigins    string
	drainDelay        time.Duration
}

func main() {
	configuration, err := parseConfig(os.Args[1:], os.LookupEnv, os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		log.Fatalf("configuration: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := execute(ctx, configuration); err != nil {
		log.Fatalf("chat server: %v", err)
	}
}

func execute(ctx context.Context, configuration config) (err error) {
	instrumentation, err := telemetry.Setup(ctx, "chatserver")
	if err != nil {
		return fmt.Errorf("set up telemetry: %w", err)
	}
	defer func() {
		flushContext, cancel := context.WithTimeout(context.Background(), telemetryFlushTimeout)
		defer cancel()
		if flushErr := instrumentation.Shutdown(flushContext); flushErr != nil {
			err = errors.Join(err, fmt.Errorf("flush telemetry: %w", flushErr))
		}
	}()

	logger := slog.NewLogLogger(instrumentation.Handler, slog.LevelInfo)
	return run(ctx, configuration, logger)
}

// parseConfig reads flags whose defaults come from the environment, so the
// same binary is configured by flags locally and by env vars in containers.
func parseConfig(args []string, lookupEnv func(string) (string, bool), output io.Writer) (config, error) {
	env := func(key, fallback string) string {
		if value, ok := lookupEnv(key); ok {
			return value
		}
		return fallback
	}
	envDuration := func(key string, fallback time.Duration) (time.Duration, error) {
		value, ok := lookupEnv(key)
		if !ok {
			return fallback, nil
		}
		duration, err := time.ParseDuration(value)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", key, err)
		}
		return duration, nil
	}

	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "chatserver"
	}
	heartbeatInterval, err := envDuration("CHAT_HEARTBEAT_INTERVAL", 10*time.Second)
	if err != nil {
		return config{}, err
	}
	drainDelay, err := envDuration("CHAT_DRAIN_DELAY", 0)
	if err != nil {
		return config{}, err
	}

	configuration := config{
		name:              env("CHAT_SERVER_NAME", hostname),
		listenAddress:     env("CHAT_LISTEN", "127.0.0.1:8080"),
		advertisedURL:     env("CHAT_ADVERTISE", "ws://127.0.0.1:8080"),
		databaseURL:       env("DATABASE_URL", "postgres://chat:chat@127.0.0.1:5432/chat?sslmode=disable"),
		redisAddress:      env("REDIS_ADDR", "127.0.0.1:6379"),
		redisChannel:      env("REDIS_CHANNEL", "chat-messages"),
		registryBaseURL:   env("REGISTRY_URL", "http://127.0.0.1:9000"),
		heartbeatInterval: heartbeatInterval,
		allowedOrigins:    env("CHAT_ALLOWED_ORIGINS", "http://localhost:5173,http://127.0.0.1:5173"),
		drainDelay:        drainDelay,
	}

	flags := flag.NewFlagSet("chatserver", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&configuration.name, "name", configuration.name, "server name stored with messages and logs (env CHAT_SERVER_NAME)")
	flags.StringVar(&configuration.listenAddress, "listen", configuration.listenAddress, "TCP address the chat server listens on (env CHAT_LISTEN)")
	flags.StringVar(&configuration.advertisedURL, "advertise", configuration.advertisedURL, "WebSocket base URL or same-origin path advertised to clients (env CHAT_ADVERTISE)")
	flags.StringVar(&configuration.databaseURL, "db", configuration.databaseURL, "PostgreSQL connection URL (env DATABASE_URL)")
	flags.StringVar(&configuration.redisAddress, "redis", configuration.redisAddress, "Redis address (env REDIS_ADDR)")
	flags.StringVar(&configuration.redisChannel, "redis-channel", configuration.redisChannel, "Redis pub/sub channel (env REDIS_CHANNEL)")
	flags.StringVar(&configuration.registryBaseURL, "registry", configuration.registryBaseURL, "load-balancer registry base URL; empty disables registration (env REGISTRY_URL)")
	flags.DurationVar(&configuration.heartbeatInterval, "heartbeat", configuration.heartbeatInterval, "interval between load reports to the registry (env CHAT_HEARTBEAT_INTERVAL)")
	flags.StringVar(&configuration.allowedOrigins, "allowed-origins", configuration.allowedOrigins, "comma-separated browser origins allowed besides same-origin (env CHAT_ALLOWED_ORIGINS)")
	flags.DurationVar(&configuration.drainDelay, "drain-delay", configuration.drainDelay, "time to stay up but unready before closing connections on shutdown (env CHAT_DRAIN_DELAY)")

	if err := flags.Parse(args); err != nil {
		return config{}, err
	}
	if flags.NArg() != 0 {
		return config{}, fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	if err := validateConfig(configuration); err != nil {
		return config{}, err
	}
	return configuration, nil
}

func validateConfig(configuration config) error {
	if strings.TrimSpace(configuration.name) == "" {
		return errors.New("name must not be empty")
	}
	if err := validateTCPAddress("listen", configuration.listenAddress); err != nil {
		return err
	}
	if err := validateTCPAddress("redis", configuration.redisAddress); err != nil {
		return err
	}
	if strings.TrimSpace(configuration.databaseURL) == "" {
		return errors.New("db must not be empty")
	}
	if _, err := pgxpool.ParseConfig(configuration.databaseURL); err != nil {
		return errors.New("db is not a valid PostgreSQL connection URL")
	}
	if strings.TrimSpace(configuration.redisChannel) == "" {
		return errors.New("redis-channel must not be empty")
	}
	if err := validateAdvertisedURL(configuration.advertisedURL); err != nil {
		return err
	}
	if configuration.registryBaseURL != "" {
		if err := validateURL("registry", configuration.registryBaseURL, "http", "https"); err != nil {
			return err
		}
	}
	if configuration.heartbeatInterval <= 0 {
		return errors.New("heartbeat must be positive")
	}
	if configuration.drainDelay < 0 {
		return errors.New("drain-delay must not be negative")
	}
	if _, err := httpapi.ParseOrigins(configuration.allowedOrigins); err != nil {
		return fmt.Errorf("allowed-origins: %w", err)
	}
	return nil
}

func validateTCPAddress(name, address string) error {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%s address %q: %w", name, address, err)
	}
	if strings.TrimSpace(host) == "" {
		return fmt.Errorf("%s address must include a host", name)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("%s address has invalid port %q", name, portText)
	}
	return nil
}

// validateAdvertisedURL accepts an absolute ws/wss URL or a same-origin path
// such as /cs/1 that a reverse proxy routes to this server.
func validateAdvertisedURL(value string) error {
	if strings.HasPrefix(value, "/") {
		if strings.HasPrefix(value, "//") || strings.ContainsAny(value, "?# ") {
			return fmt.Errorf("advertise path %q must be a plain absolute path", value)
		}
		return nil
	}
	return validateURL("advertise", value, "ws", "wss")
}

func validateURL(name, value string, allowedSchemes ...string) error {
	parsed, err := url.ParseRequestURI(value)
	if err != nil {
		return fmt.Errorf("%s URL %q: %w", name, value, err)
	}
	if parsed.Host == "" {
		return fmt.Errorf("%s URL %q must include a host", name, value)
	}
	for _, scheme := range allowedSchemes {
		if parsed.Scheme == scheme {
			return nil
		}
	}
	return fmt.Errorf("%s URL %q has unsupported scheme %q", name, value, parsed.Scheme)
}

func run(parent context.Context, configuration config, logger *log.Logger) (runErr error) {
	poolConfig, err := pgxpool.ParseConfig(configuration.databaseURL)
	if err != nil {
		return fmt.Errorf("parse database URL: %w", err)
	}
	poolConfig.ConnConfig.Tracer = otelpgx.NewTracer()
	store, err := postgres.Open(parent, poolConfig)
	if err != nil {
		return fmt.Errorf("open message store: %w", err)
	}
	defer store.Close()

	redisClient := redis.NewClient(&redis.Options{Addr: configuration.redisAddress})
	defer func() {
		if err := redisClient.Close(); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("close Redis client: %w", err))
		}
	}()
	if err := redisotel.InstrumentTracing(redisClient); err != nil {
		return fmt.Errorf("instrument Redis tracing: %w", err)
	}
	if err := redisotel.InstrumentMetrics(redisClient); err != nil {
		return fmt.Errorf("instrument Redis metrics: %w", err)
	}
	if err := redisClient.Ping(parent).Err(); err != nil {
		return fmt.Errorf("connect to Redis at %s: %w", configuration.redisAddress, err)
	}

	broker, err := chat.NewRedisBroker(redisClient, configuration.redisChannel, logger)
	if err != nil {
		return err
	}

	// The service context outlives the shutdown signal so the server can stay
	// up, unready, during the drain delay; stopService ends it explicitly.
	serviceContext, stopService := context.WithCancel(context.WithoutCancel(parent))
	defer stopService()
	subscription, err := broker.Subscribe(serviceContext)
	if err != nil {
		return err
	}
	defer func() {
		stopService()
		subscription.Wait()
	}()

	origins, err := httpapi.ParseOrigins(configuration.allowedOrigins)
	if err != nil {
		return fmt.Errorf("parse allowed origins: %w", err)
	}

	var reporter chat.LoadReporter = noopReporter{}
	var heartbeat *registry.Heartbeat
	if configuration.registryBaseURL != "" {
		httpClient := &http.Client{Timeout: registryRequestTimeout}
		registryClient, err := registry.NewClient(configuration.registryBaseURL, configuration.advertisedURL, httpClient)
		if err != nil {
			return fmt.Errorf("create registry client: %w", err)
		}
		heartbeat, err = registry.NewHeartbeat(registryClient, configuration.heartbeatInterval, logger)
		if err != nil {
			return fmt.Errorf("create registry heartbeat: %w", err)
		}
		reporter = heartbeat
	}

	hub := chat.NewHub(configuration.name, store, broker, reporter, logger)
	chatHandler := chat.NewHandler(hub, store, origins.Allows, logger)

	var draining atomic.Bool
	mux := http.NewServeMux()
	mux.Handle("/", chatHandler.Routes())
	mux.Handle("GET /healthz", httpapi.Liveness())
	mux.Handle("GET /readyz", httpapi.Readiness(&draining, map[string]httpapi.Check{
		"postgres": store.Ping,
		"redis":    func(ctx context.Context) error { return redisClient.Ping(ctx).Err() },
	}))

	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(parent, "tcp", configuration.listenAddress)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", configuration.listenAddress, err)
	}

	server := &http.Server{
		Handler:           otelhttp.NewHandler(httpapi.CORS(origins, mux), "chatserver", otelhttp.WithFilter(skipProbes)),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		BaseContext: func(net.Listener) context.Context {
			return serviceContext
		},
	}

	heartbeatDone := make(chan struct{})
	if heartbeat != nil {
		if err := heartbeat.UpdateLoad(serviceContext, 0); err != nil {
			logger.Printf("initial registration failed, heartbeat will retry: %v", err)
		}
		go func() {
			defer close(heartbeatDone)
			heartbeat.Run(serviceContext)
		}()
	} else {
		close(heartbeatDone)
	}

	hubErrors := make(chan error, 1)
	go func() {
		hubErrors <- hub.Run(serviceContext, subscription.Messages())
	}()

	serverErrors := make(chan error, 1)
	go func() {
		err := server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serverErrors <- err
	}()

	logger.Printf("chat server %s listening on %s and advertising %s", configuration.name, listener.Addr(), configuration.advertisedURL)

	var eventErr error
	var hubStopped, serverStopped bool
	select {
	case <-parent.Done():
	case err := <-hubErrors:
		hubStopped = true
		if err != nil {
			eventErr = fmt.Errorf("run chat hub: %w", err)
		}
	case err := <-serverErrors:
		serverStopped = true
		if err != nil {
			eventErr = fmt.Errorf("serve chat HTTP: %w", err)
		}
	}

	draining.Store(true)
	if heartbeat != nil {
		deregisterContext, cancelDeregister := context.WithTimeout(context.Background(), deregisterTimeout)
		if err := heartbeat.Deregister(deregisterContext); err != nil {
			logger.Printf("deregister from load balancer: %v", err)
		}
		cancelDeregister()
	}
	if eventErr == nil && configuration.drainDelay > 0 {
		logger.Printf("draining for %s before closing client connections", configuration.drainDelay)
		time.Sleep(configuration.drainDelay)
	}

	stopService()
	<-heartbeatDone
	shutdownErr := shutdownServer(server)
	if !hubStopped {
		if err := <-hubErrors; err != nil {
			eventErr = errors.Join(eventErr, fmt.Errorf("stop chat hub: %w", err))
		}
	}
	if !serverStopped {
		if err := <-serverErrors; err != nil {
			eventErr = errors.Join(eventErr, fmt.Errorf("stop chat HTTP: %w", err))
		}
	}

	return errors.Join(eventErr, shutdownErr)
}

func shutdownServer(server *http.Server) error {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		shutdownErr := fmt.Errorf("shut down chat HTTP server: %w", err)
		if closeErr := server.Close(); closeErr != nil {
			return errors.Join(shutdownErr, fmt.Errorf("force close chat HTTP server: %w", closeErr))
		}
		return shutdownErr
	}
	return nil
}

// skipProbes keeps orchestrator health checks out of the traces.
func skipProbes(r *http.Request) bool {
	return r.URL.Path != "/healthz" && r.URL.Path != "/readyz"
}

// noopReporter is used when the server runs behind a platform load balancer
// such as a Kubernetes Service and has no registry to report to.
type noopReporter struct{}

func (noopReporter) UpdateLoad(context.Context, int) error { return nil }
