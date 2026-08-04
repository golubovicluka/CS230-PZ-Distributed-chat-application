package main

import (
	"io"
	"testing"
)

func TestParseConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{name: "defaults are valid"},
		{name: "explicit values are valid", args: []string{"-listen", "0.0.0.0:8081", "-advertise", "wss://chat.example.com", "-redis", "redis.example.com:6379", "-registry", "https://registry.example.com", "-db", "file:test.db", "-redis-channel", "test-chat"}},
		{name: "rejects invalid listen address", args: []string{"-listen", "localhost"}, wantErr: true},
		{name: "rejects empty listen host", args: []string{"-listen", ":8080"}, wantErr: true},
		{name: "rejects invalid listen port", args: []string{"-listen", "localhost:70000"}, wantErr: true},
		{name: "rejects invalid Redis address", args: []string{"-redis", "localhost"}, wantErr: true},
		{name: "rejects empty database DSN", args: []string{"-db", ""}, wantErr: true},
		{name: "rejects empty Redis channel", args: []string{"-redis-channel", " "}, wantErr: true},
		{name: "rejects non-WebSocket advertised URL", args: []string{"-advertise", "http://localhost:8080"}, wantErr: true},
		{name: "rejects non-HTTP registry URL", args: []string{"-registry", "ws://localhost:9000"}, wantErr: true},
		{name: "rejects positional arguments", args: []string{"unexpected"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseConfig(tt.args, io.Discard)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseConfig() error = %v, wantErr %t", err, tt.wantErr)
			}
		})
	}
}
