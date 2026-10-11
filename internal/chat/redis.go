package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// envelope is the broker wire format. It carries W3C trace context next to
// the message so the delivery on every other server joins the sender's trace.
// Only Message is forwarded to browsers.
type envelope struct {
	Message json.RawMessage        `json:"message"`
	Trace   propagation.MapCarrier `json:"trace,omitempty"`
}

// RedisBroker adapts Redis pub/sub to typed chat messages and byte broadcasts.
type RedisBroker struct {
	client  redis.UniversalClient
	channel string
	logger  *log.Logger
}

func NewRedisBroker(client redis.UniversalClient, channel string, logger *log.Logger) (*RedisBroker, error) {
	if client == nil {
		return nil, errors.New("redis broker: client is required")
	}
	if strings.TrimSpace(channel) == "" {
		return nil, errors.New("redis broker: channel is required")
	}
	if logger == nil {
		return nil, errors.New("redis broker: logger is required")
	}

	return &RedisBroker{client: client, channel: channel, logger: logger}, nil
}

func (b *RedisBroker) Publish(ctx context.Context, message Message) error {
	payload, err := wrap(ctx, message)
	if err != nil {
		return err
	}
	if err := b.client.Publish(ctx, b.channel, payload).Err(); err != nil {
		return fmt.Errorf("publish Redis message: %w", err)
	}
	return nil
}

// Subscription exposes the broker payload stream and its lifecycle.
type Subscription struct {
	messages <-chan []byte
	done     <-chan struct{}
}

func (s *Subscription) Messages() <-chan []byte {
	return s.messages
}

func (s *Subscription) Wait() {
	<-s.done
}

func (b *RedisBroker) Subscribe(ctx context.Context) (*Subscription, error) {
	pubsub := b.client.Subscribe(ctx, b.channel)
	if _, err := pubsub.Receive(ctx); err != nil {
		subscribeErr := fmt.Errorf("subscribe to Redis channel %q: %w", b.channel, err)
		if closeErr := pubsub.Close(); closeErr != nil {
			return nil, errors.Join(subscribeErr, fmt.Errorf("close failed Redis subscription: %w", closeErr))
		}
		return nil, subscribeErr
	}

	redisMessages := pubsub.Channel()
	messages := make(chan []byte)
	done := make(chan struct{})

	go func() {
		defer close(done)
		defer close(messages)
		defer func() {
			if err := pubsub.Close(); err != nil {
				b.logger.Printf("close Redis subscription: %v", err)
			}
		}()

		for {
			select {
			case <-ctx.Done():
				return
			case message, ok := <-redisMessages:
				if !ok {
					return
				}
				if !b.forward(ctx, []byte(message.Payload), messages) {
					return
				}
			}
		}
	}()

	return &Subscription{messages: messages, done: done}, nil
}

// forward unwraps one broker payload, records its fan-out latency inside the
// sender's trace, and hands the browser-facing message to the hub. It returns
// false when ctx ends first.
func (b *RedisBroker) forward(ctx context.Context, payload []byte, messages chan<- []byte) bool {
	message, carrier := unwrap(payload)
	spanContext := otel.GetTextMapPropagator().Extract(ctx, carrier)
	_, span := tracer.Start(spanContext, "chat.message.fanout",
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			attribute.String("messaging.system", "redis"),
			attribute.String("messaging.destination.name", b.channel),
		))
	defer span.End()

	var decoded Message
	if err := json.Unmarshal(message, &decoded); err == nil {
		if stored, err := time.Parse(time.RFC3339Nano, decoded.Timestamp); err == nil {
			fanoutDuration.Record(ctx, time.Since(stored).Seconds())
		}
		span.SetAttributes(attribute.String("chat.origin_server", decoded.Server))
	}

	select {
	case messages <- message:
		return true
	case <-ctx.Done():
		return false
	}
}

func wrap(ctx context.Context, message Message) ([]byte, error) {
	encoded, err := json.Marshal(message)
	if err != nil {
		return nil, fmt.Errorf("encode Redis message: %w", err)
	}
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	payload, err := json.Marshal(envelope{Message: encoded, Trace: carrier})
	if err != nil {
		return nil, fmt.Errorf("encode Redis envelope: %w", err)
	}
	return payload, nil
}

// unwrap accepts both the envelope and a bare message, so this release can
// still consume payloads published in the original bare-message format.
func unwrap(payload []byte) (json.RawMessage, propagation.MapCarrier) {
	var wrapped envelope
	if err := json.Unmarshal(payload, &wrapped); err == nil && len(wrapped.Message) > 0 {
		return wrapped.Message, wrapped.Trace
	}
	return payload, propagation.MapCarrier{}
}
