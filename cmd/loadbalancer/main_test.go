package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"io"
	"log"
	"strings"
	"testing"
	"time"
)

func TestParseConfig(t *testing.T) {
	testCases := []struct {
		name        string
		args        []string
		want        config
		wantError   string
		wantFlagErr error
	}{
		{
			name: "defaults",
			want: defaultConfig(),
		},
		{
			name: "explicit values",
			args: []string{
				"-address=127.0.0.1:9100",
				"-allowed-origins=https://chat.example.com",
				"-server-ttl=6s",
				"-read-header-timeout=1s",
				"-read-timeout=2s",
				"-write-timeout=3s",
				"-idle-timeout=4s",
				"-shutdown-timeout=5s",
			},
			want: config{
				address:           "127.0.0.1:9100",
				allowedOrigins:    "https://chat.example.com",
				serverTTL:         6 * time.Second,
				readHeaderTimeout: time.Second,
				readTimeout:       2 * time.Second,
				writeTimeout:      3 * time.Second,
				idleTimeout:       4 * time.Second,
				shutdownTimeout:   5 * time.Second,
			},
		},
		{name: "empty address", args: []string{"-address= "}, wantError: "address must not be empty"},
		{name: "invalid origin", args: []string{"-allowed-origins=chat.example.com"}, wantError: "allowed-origins"},
		{name: "zero server ttl", args: []string{"-server-ttl=0s"}, wantError: "server-ttl must be positive"},
		{name: "zero timeout", args: []string{"-read-timeout=0s"}, wantError: "read-timeout must be positive"},
		{name: "negative timeout", args: []string{"-shutdown-timeout=-1s"}, wantError: "shutdown-timeout must be positive"},
		{name: "invalid duration", args: []string{"-idle-timeout=soon"}, wantError: "parse flags"},
		{name: "positional argument", args: []string{"extra"}, wantError: "unexpected positional arguments"},
		{name: "help", args: []string{"-h"}, wantError: "parse flags", wantFlagErr: flag.ErrHelp},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var output bytes.Buffer
			got, err := parseConfig(testCase.args, &output)
			if testCase.wantError != "" {
				if err == nil {
					t.Fatalf("parseConfig() error = nil, want an error containing %q", testCase.wantError)
				}
				if !strings.Contains(err.Error(), testCase.wantError) {
					t.Errorf("parseConfig() error = %q, want it to contain %q", err, testCase.wantError)
				}
				if testCase.wantFlagErr != nil && !errors.Is(err, testCase.wantFlagErr) {
					t.Errorf("parseConfig() error = %v, want it to wrap %v", err, testCase.wantFlagErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("parseConfig() error = %v", err)
			}
			if got != testCase.want {
				t.Errorf("parseConfig() = %+v, want %+v", got, testCase.want)
			}
		})
	}
}

func TestConfigValidateRejectsEveryNonPositiveTimeout(t *testing.T) {
	testCases := []struct {
		name   string
		change func(*config)
	}{
		{name: "server ttl", change: func(configuration *config) { configuration.serverTTL = 0 }},
		{name: "read header", change: func(configuration *config) { configuration.readHeaderTimeout = 0 }},
		{name: "read", change: func(configuration *config) { configuration.readTimeout = 0 }},
		{name: "write", change: func(configuration *config) { configuration.writeTimeout = 0 }},
		{name: "idle", change: func(configuration *config) { configuration.idleTimeout = 0 }},
		{name: "shutdown", change: func(configuration *config) { configuration.shutdownTimeout = 0 }},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			configuration := defaultConfig()
			testCase.change(&configuration)
			if err := configuration.validate(); err == nil {
				t.Fatal("validate() error = nil, want a non-positive timeout error")
			}
		})
	}
}

func TestRunRejectsInvalidDependenciesAndConfig(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	testCases := []struct {
		name          string
		ctx           context.Context
		configuration config
		logger        *log.Logger
		wantError     string
	}{
		{name: "nil context", configuration: defaultConfig(), logger: logger, wantError: "context must not be nil"},
		{name: "nil logger", ctx: context.Background(), configuration: defaultConfig(), wantError: "logger must not be nil"},
		{name: "invalid config", ctx: context.Background(), configuration: config{}, logger: logger, wantError: "validate configuration"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := run(testCase.ctx, testCase.configuration, testCase.logger)
			if err == nil {
				t.Fatalf("run() error = nil, want an error containing %q", testCase.wantError)
			}
			if !strings.Contains(err.Error(), testCase.wantError) {
				t.Errorf("run() error = %q, want it to contain %q", err, testCase.wantError)
			}
		})
	}
}

func TestRunReturnsServeError(t *testing.T) {
	configuration := defaultConfig()
	configuration.address = "127.0.0.1:not-a-port"

	err := run(context.Background(), configuration, log.New(io.Discard, "", 0))
	if err == nil {
		t.Fatal("run() error = nil, want listen error")
	}
	if !strings.Contains(err.Error(), "listen on") {
		t.Errorf("run() error = %q, want it to contain %q", err, "listen on")
	}
}

func TestRunShutsDownWhenContextEnds(t *testing.T) {
	configuration := defaultConfig()
	configuration.address = "127.0.0.1:0"
	configuration.shutdownTimeout = time.Second
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := run(ctx, configuration, log.New(io.Discard, "", 0)); err != nil {
		t.Fatalf("run() error = %v, want graceful shutdown", err)
	}
}
