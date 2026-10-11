package main

import (
	"errors"
	"flag"
	"io"
	"testing"
	"time"
)

func TestParseConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		env     map[string]string
		wantErr bool
	}{
		{name: "defaults are valid"},
		{name: "explicit values are valid", args: []string{"-name", "chat-1", "-listen", "0.0.0.0:8081", "-advertise", "wss://chat.example.com", "-redis", "redis.example.com:6379", "-registry", "https://registry.example.com", "-db", "postgres://chat:chat@db.example.com:5432/chat", "-redis-channel", "test-chat", "-heartbeat", "5s", "-allowed-origins", "https://chat.example.com", "-drain-delay", "5s"}},
		{name: "same-origin advertise path is valid", args: []string{"-advertise", "/cs/1"}},
		{name: "empty registry disables registration", args: []string{"-registry", ""}},
		{name: "environment provides values", env: map[string]string{"CHAT_LISTEN": "0.0.0.0:9090", "REGISTRY_URL": "", "CHAT_DRAIN_DELAY": "3s"}},
		{name: "rejects invalid environment duration", env: map[string]string{"CHAT_HEARTBEAT_INTERVAL": "soon"}, wantErr: true},
		{name: "rejects invalid listen address", args: []string{"-listen", "localhost"}, wantErr: true},
		{name: "rejects empty listen host", args: []string{"-listen", ":8080"}, wantErr: true},
		{name: "rejects invalid listen port", args: []string{"-listen", "localhost:70000"}, wantErr: true},
		{name: "rejects invalid Redis address", args: []string{"-redis", "localhost"}, wantErr: true},
		{name: "rejects empty database URL", args: []string{"-db", ""}, wantErr: true},
		{name: "rejects malformed database URL", args: []string{"-db", "postgres://%zz"}, wantErr: true},
		{name: "rejects empty Redis channel", args: []string{"-redis-channel", " "}, wantErr: true},
		{name: "rejects empty name", args: []string{"-name", " "}, wantErr: true},
		{name: "rejects non-WebSocket advertised URL", args: []string{"-advertise", "http://localhost:8080"}, wantErr: true},
		{name: "rejects protocol-relative advertise path", args: []string{"-advertise", "//evil.example/ws"}, wantErr: true},
		{name: "rejects advertise path with query", args: []string{"-advertise", "/cs/1?x=1"}, wantErr: true},
		{name: "rejects non-HTTP registry URL", args: []string{"-registry", "ws://localhost:9000"}, wantErr: true},
		{name: "rejects zero heartbeat", args: []string{"-heartbeat", "0s"}, wantErr: true},
		{name: "rejects negative drain delay", args: []string{"-drain-delay", "-1s"}, wantErr: true},
		{name: "rejects invalid origin", args: []string{"-allowed-origins", "localhost:5173"}, wantErr: true},
		{name: "rejects positional arguments", args: []string{"unexpected"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseConfig(tt.args, lookupFrom(tt.env), io.Discard)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseConfig() error = %v, wantErr %t", err, tt.wantErr)
			}
		})
	}
}

func TestParseConfigPrecedence(t *testing.T) {
	t.Parallel()

	env := map[string]string{"CHAT_LISTEN": "0.0.0.0:9090", "REGISTRY_URL": "", "CHAT_DRAIN_DELAY": "3s"}
	configuration, err := parseConfig([]string{"-listen", "0.0.0.0:7070"}, lookupFrom(env), io.Discard)
	if err != nil {
		t.Fatalf("parseConfig() error = %v", err)
	}
	if configuration.listenAddress != "0.0.0.0:7070" {
		t.Errorf("listen = %q, want flag to override environment", configuration.listenAddress)
	}
	if configuration.registryBaseURL != "" {
		t.Errorf("registry = %q, want empty value from environment", configuration.registryBaseURL)
	}
	if configuration.drainDelay != 3*time.Second {
		t.Errorf("drain delay = %s, want 3s from environment", configuration.drainDelay)
	}
}

func TestParseConfigHelp(t *testing.T) {
	t.Parallel()

	if _, err := parseConfig([]string{"-h"}, lookupFrom(nil), io.Discard); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("parseConfig(-h) error = %v, want flag.ErrHelp", err)
	}
}

func lookupFrom(env map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := env[key]
		return value, ok
	}
}
