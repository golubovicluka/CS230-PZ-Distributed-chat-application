package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/go-redis/redis/v8"
)

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
	payload, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("encode Redis message: %w", err)
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
				payload := []byte(message.Payload)
				select {
				case messages <- payload:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return &Subscription{messages: messages, done: done}, nil
}
