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
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/golubovicluka/CS230-PZ-Distributed-chat-application/internal/httpapi"
	"github.com/golubovicluka/CS230-PZ-Distributed-chat-application/internal/loadbalancer"
)

type config struct {
	address           string
	readHeaderTimeout time.Duration
	readTimeout       time.Duration
	writeTimeout      time.Duration
	idleTimeout       time.Duration
	shutdownTimeout   time.Duration
}

func defaultConfig() config {
	return config{
		address:           ":9000",
		readHeaderTimeout: 5 * time.Second,
		readTimeout:       15 * time.Second,
		writeTimeout:      15 * time.Second,
		idleTimeout:       60 * time.Second,
		shutdownTimeout:   10 * time.Second,
	}
}

func main() {
	logger := log.New(os.Stderr, "[LB] ", log.LstdFlags)
	if err := execute(os.Args[1:], os.Stderr, logger); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		logger.Printf("stopped: %v", err)
		os.Exit(1)
	}
}

func execute(args []string, flagOutput io.Writer, logger *log.Logger) error {
	configuration, err := parseConfig(args, flagOutput)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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

	timeouts := []struct {
		name  string
		value time.Duration
	}{
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

	balancer := loadbalancer.New(logger)
	server := &http.Server{
		Addr:              configuration.address,
		Handler:           httpapi.CORS(balancer.Handler()),
		ReadHeaderTimeout: configuration.readHeaderTimeout,
		ReadTimeout:       configuration.readTimeout,
		WriteTimeout:      configuration.writeTimeout,
		IdleTimeout:       configuration.idleTimeout,
		ErrorLog:          logger,
	}
	listener, err := net.Listen("tcp", configuration.address)
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
