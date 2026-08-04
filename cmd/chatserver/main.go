package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/golubovicluka/CS230-PZ-Distributed-chat-application/internal/chat"
	"github.com/golubovicluka/CS230-PZ-Distributed-chat-application/internal/httpapi"
	"github.com/golubovicluka/CS230-PZ-Distributed-chat-application/internal/registry"
	"github.com/golubovicluka/CS230-PZ-Distributed-chat-application/internal/sqlite"
)

const (
	registryRequestTimeout = 3 * time.Second
	shutdownTimeout        = 5 * time.Second
)

type config struct {
	listenAddress   string
	advertisedURL   string
	databaseDSN     string
	redisAddress    string
	redisChannel    string
	registryBaseURL string
}

func main() {
	configuration, err := parseConfig(os.Args[1:], os.Stderr)
	if err != nil {
		log.Fatalf("configuration: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, configuration, log.Default()); err != nil {
		log.Fatalf("chat server: %v", err)
	}
}

func parseConfig(args []string, output io.Writer) (config, error) {
	flags := flag.NewFlagSet("chatserver", flag.ContinueOnError)
	flags.SetOutput(output)

	configuration := config{}
	flags.StringVar(&configuration.listenAddress, "listen", "127.0.0.1:8080", "TCP address the chat server listens on")
	flags.StringVar(&configuration.advertisedURL, "advertise", "ws://127.0.0.1:8080", "WebSocket base URL advertised to clients")
	flags.StringVar(&configuration.databaseDSN, "db", "file:chat.db?_journal_mode=WAL&_busy_timeout=5000", "SQLite data source name")
	flags.StringVar(&configuration.redisAddress, "redis", "127.0.0.1:6379", "Redis address")
	flags.StringVar(&configuration.redisChannel, "redis-channel", "chat-messages", "Redis pub/sub channel")
	flags.StringVar(&configuration.registryBaseURL, "registry", "http://127.0.0.1:9000", "Load-balancer registry base URL")

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
	if err := validateTCPAddress("listen", configuration.listenAddress); err != nil {
		return err
	}
	if err := validateTCPAddress("redis", configuration.redisAddress); err != nil {
		return err
	}
	if strings.TrimSpace(configuration.databaseDSN) == "" {
		return errors.New("db must not be empty")
	}
	if strings.TrimSpace(configuration.redisChannel) == "" {
		return errors.New("redis-channel must not be empty")
	}
	if err := validateURL("advertise", configuration.advertisedURL, "ws", "wss"); err != nil {
		return err
	}
	if err := validateURL("registry", configuration.registryBaseURL, "http", "https"); err != nil {
		return err
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
	store, err := sqlite.Open(configuration.databaseDSN)
	if err != nil {
		return fmt.Errorf("open message store: %w", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("close message store: %w", err))
		}
	}()

	redisClient := redis.NewClient(&redis.Options{Addr: configuration.redisAddress})
	defer func() {
		if err := redisClient.Close(); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("close Redis client: %w", err))
		}
	}()
	if err := redisClient.Ping(parent).Err(); err != nil {
		return fmt.Errorf("connect to Redis at %s: %w", configuration.redisAddress, err)
	}

	broker, err := chat.NewRedisBroker(redisClient, configuration.redisChannel, logger)
	if err != nil {
		return err
	}

	serviceContext, stopService := context.WithCancel(parent)
	subscription, err := broker.Subscribe(serviceContext)
	if err != nil {
		stopService()
		return err
	}
	defer func() {
		stopService()
		subscription.Wait()
	}()

	httpClient := &http.Client{Timeout: registryRequestTimeout}
	registryClient, err := registry.NewClient(configuration.registryBaseURL, configuration.advertisedURL, httpClient)
	if err != nil {
		return fmt.Errorf("create registry client: %w", err)
	}

	hub := chat.NewHub(configuration.advertisedURL, store, broker, registryClient, logger)
	chatHandler := chat.NewHandler(hub, store, func(*http.Request) bool { return true }, logger)
	listener, err := net.Listen("tcp", configuration.listenAddress)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", configuration.listenAddress, err)
	}

	server := &http.Server{
		Handler:           httpapi.CORS(chatHandler.Routes()),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		BaseContext: func(net.Listener) context.Context {
			return serviceContext
		},
	}
	if err := registryClient.Register(serviceContext); err != nil {
		stopService()
		registerErr := fmt.Errorf("register chat server: %w", err)
		if closeErr := listener.Close(); closeErr != nil {
			return errors.Join(registerErr, fmt.Errorf("close unregistered chat listener: %w", closeErr))
		}
		return registerErr
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

	logger.Printf("[ChatServer] listening on %s and advertising %s", listener.Addr(), configuration.advertisedURL)

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

	stopService()
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
