package chat

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

const instrumentationName = "github.com/golubovicluka/CS230-PZ-Distributed-chat-application/internal/chat"

// Instruments come from the global providers, which delegate to the real SDK
// once telemetry.Setup installs it and stay no-ops in tests.
var (
	tracer = otel.Tracer(instrumentationName)
	meter  = otel.Meter(instrumentationName)

	connectionsActive, _ = meter.Int64UpDownCounter("chat.connections.active",
		metric.WithDescription("WebSocket clients currently connected to this server."),
		metric.WithUnit("{connection}"))
	messagesReceived, _ = meter.Int64Counter("chat.messages.received",
		metric.WithDescription("Messages accepted from local clients and stored."),
		metric.WithUnit("{message}"))
	messagesDelivered, _ = meter.Int64Counter("chat.messages.delivered",
		metric.WithDescription("Messages queued for delivery to local clients."),
		metric.WithUnit("{message}"))
	clientsDropped, _ = meter.Int64Counter("chat.clients.dropped",
		metric.WithDescription("Clients disconnected because they could not keep up."),
		metric.WithUnit("{connection}"))
	fanoutDuration, _ = meter.Float64Histogram("chat.message.fanout.duration",
		metric.WithDescription("Time from a message being stored to it arriving at this server through the broker."),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5))
)
