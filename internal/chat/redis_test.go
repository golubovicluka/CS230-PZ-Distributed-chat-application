package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
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

func TestEnvelopeCarriesTraceToFanout(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	parent, span := provider.Tracer("test").Start(context.Background(), "sender")
	message := Message{ID: 3, Username: "alice", Content: "hello", Server: "chat-1", Timestamp: time.Now().UTC().Format(time.RFC3339Nano)}
	payload, err := wrap(parent, message)
	span.End()
	if err != nil {
		t.Fatalf("wrap() error = %v", err)
	}

	broker := &RedisBroker{channel: "chat-messages"}
	forwarded := make(chan []byte, 1)
	if !broker.forward(context.Background(), payload, forwarded) {
		t.Fatal("forward() returned false with a live context")
	}

	var received Message
	if err := json.Unmarshal(<-forwarded, &received); err != nil {
		t.Fatalf("forwarded payload is not a bare message: %v", err)
	}
	if received != message {
		t.Errorf("forwarded message = %+v, want %+v", received, message)
	}

	var fanout sdktrace.ReadOnlySpan
	for _, ended := range recorder.Ended() {
		if ended.Name() == "chat.message.fanout" {
			fanout = ended
		}
	}
	if fanout == nil {
		t.Fatal("no chat.message.fanout span was recorded")
	}
	if fanout.SpanContext().TraceID() != span.SpanContext().TraceID() {
		t.Errorf("fan-out trace = %s, want sender trace %s", fanout.SpanContext().TraceID(), span.SpanContext().TraceID())
	}
	if fanout.Parent().SpanID() != span.SpanContext().SpanID() {
		t.Errorf("fan-out parent = %s, want sender span %s", fanout.Parent().SpanID(), span.SpanContext().SpanID())
	}
}

func TestUnwrapAcceptsLegacyBareMessages(t *testing.T) {
	t.Parallel()

	legacy := []byte(`{"id":1,"username":"bob","content":"hi","server":"chat-2"}`)
	message, carrier := unwrap(legacy)
	if string(message) != string(legacy) {
		t.Errorf("unwrap() message = %s, want the original payload", message)
	}
	if len(carrier) != 0 {
		t.Errorf("unwrap() carrier = %v, want empty", carrier)
	}
}

func TestForwardStopsWhenContextEnds(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	broker := &RedisBroker{channel: "chat-messages"}
	if broker.forward(ctx, []byte(`{"message":{"id":1}}`), make(chan []byte)) {
		t.Fatal("forward() returned true after the context ended")
	}
}

func TestRedisBrokerRoundTrip(t *testing.T) {
	address := os.Getenv("TEST_REDIS_ADDR")
	if address == "" {
		t.Skip("TEST_REDIS_ADDR is not set; skipping Redis integration test")
	}

	client := redis.NewClient(&redis.Options{Addr: address})
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("close Redis client: %v", err)
		}
	})
	channel := fmt.Sprintf("chat-test-%d", time.Now().UnixNano())
	broker, err := NewRedisBroker(client, channel, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatalf("NewRedisBroker() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	subscription, err := broker.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}

	sent := Message{ID: 9, Username: "alice", Content: "across servers", Server: "chat-1", Timestamp: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := broker.Publish(ctx, sent); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	select {
	case payload := <-subscription.Messages():
		var received Message
		if err := json.Unmarshal(payload, &received); err != nil {
			t.Fatalf("decode received payload %s: %v", payload, err)
		}
		if received != sent {
			t.Errorf("received %+v, want %+v", received, sent)
		}
	case <-ctx.Done():
		t.Fatal("message did not arrive through Redis")
	}

	cancel()
	subscription.Wait()
}
