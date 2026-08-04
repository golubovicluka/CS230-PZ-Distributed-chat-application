package chat

import (
	"io"
	"log"
	"testing"

	"github.com/go-redis/redis/v8"
)

func TestNewRedisBroker(t *testing.T) {
	t.Parallel()

	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("close Redis client: %v", err)
		}
	})
	logger := log.New(io.Discard, "", 0)

	tests := []struct {
		name    string
		client  redis.UniversalClient
		channel string
		logger  *log.Logger
		wantErr bool
	}{
		{name: "valid", client: client, channel: "chat-messages", logger: logger},
		{name: "missing client", channel: "chat-messages", logger: logger, wantErr: true},
		{name: "missing channel", client: client, logger: logger, wantErr: true},
		{name: "blank channel", client: client, channel: "  ", logger: logger, wantErr: true},
		{name: "missing logger", client: client, channel: "chat-messages", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			broker, err := NewRedisBroker(tt.client, tt.channel, tt.logger)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NewRedisBroker() error = %v, wantErr %t", err, tt.wantErr)
			}
			if !tt.wantErr && broker == nil {
				t.Fatal("NewRedisBroker() returned nil broker")
			}
		})
	}
}
