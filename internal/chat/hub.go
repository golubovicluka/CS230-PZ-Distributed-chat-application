package chat

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/gorilla/websocket"
)

const closeNoticeWait = time.Second

var ErrHubStopped = errors.New("chat hub stopped")

// MessageSaver is the persistence seam used by the hub.
type MessageSaver interface {
	Save(context.Context, Message) (Message, error)
}

// MessagePublisher is the inter-process communication seam used by the hub.
type MessagePublisher interface {
	Publish(context.Context, Message) error
}

// LoadReporter reports the number of connected clients to the registry.
type LoadReporter interface {
	UpdateLoad(context.Context, int) error
}

// Hub coordinates chat clients. Run is the sole owner of client membership.
type Hub struct {
	address  string
	saver    MessageSaver
	broker   MessagePublisher
	reporter LoadReporter
	logger   *log.Logger

	register    chan *client
	unregister  chan *client
	loadUpdates chan int
	done        chan struct{}
}

func NewHub(address string, saver MessageSaver, broker MessagePublisher, reporter LoadReporter, logger *log.Logger) *Hub {
	return &Hub{
		address:     address,
		saver:       saver,
		broker:      broker,
		reporter:    reporter,
		logger:      logger,
		register:    make(chan *client),
		unregister:  make(chan *client),
		loadUpdates: make(chan int, 1),
		done:        make(chan struct{}),
	}
}

// Run owns the client map and processes lifecycle and broadcast events.
// A Hub must be run exactly once.
func (h *Hub) Run(ctx context.Context, broadcasts <-chan []byte) error {
	clients := make(map[*client]struct{})
	reporterContext, stopReporter := context.WithCancel(ctx)
	reporterDone := make(chan struct{})
	go func() {
		defer close(reporterDone)
		h.reportLoads(reporterContext)
	}()

	defer func() {
		stopReporter()
		for c := range clients {
			h.sendGoingAway(c)
			h.removeClient(clients, c)
		}
		close(h.done)
		<-reporterDone
	}()

	for {
		select {
		case <-ctx.Done():
			return nil

		case c := <-h.register:
			if _, exists := clients[c]; exists {
				continue
			}
			clients[c] = struct{}{}
			connectionsActive.Add(ctx, 1)
			load := len(clients)
			h.logger.Printf("[Server %s] client %q connected; clients=%d", h.address, c.username, load)
			h.queueLoad(load)

		case c := <-h.unregister:
			if h.removeClient(clients, c) {
				h.queueLoad(len(clients))
			}

		case payload, ok := <-broadcasts:
			if !ok {
				if ctx.Err() != nil {
					return nil
				}
				return errors.New("chat broadcast subscription closed")
			}
			loadChanged := false
			delivered := 0
			for c := range clients {
				select {
				case c.send <- payload:
					delivered++
				default:
					h.logger.Printf("[Server %s] removing slow client %q", h.address, c.username)
					h.removeClient(clients, c)
					clientsDropped.Add(ctx, 1)
					loadChanged = true
				}
			}
			messagesDelivered.Add(ctx, int64(delivered))
			if loadChanged {
				h.queueLoad(len(clients))
			}
		}
	}
}

func (h *Hub) registerClient(ctx context.Context, c *client) error {
	select {
	case h.register <- c:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("register client: %w", ctx.Err())
	case <-h.done:
		return ErrHubStopped
	}
}

func (h *Hub) unregisterClient(c *client) {
	select {
	case h.unregister <- c:
	case <-h.done:
	}
}

func (h *Hub) removeClient(clients map[*client]struct{}, c *client) bool {
	if _, exists := clients[c]; !exists {
		return false
	}

	delete(clients, c)
	connectionsActive.Add(context.Background(), -1)
	if err := c.conn.Close(); err != nil {
		h.logger.Printf("[Server %s] close client %q: %v", h.address, c.username, err)
	}
	close(c.send)
	h.logger.Printf("[Server %s] client %q disconnected; clients=%d", h.address, c.username, len(clients))
	return true
}

// sendGoingAway tells the browser the server is leaving, so it reconnects
// through the load balancer instead of treating the drop as an error.
func (h *Hub) sendGoingAway(c *client) {
	notice := websocket.FormatCloseMessage(websocket.CloseGoingAway, "server shutting down")
	if err := c.conn.WriteControl(websocket.CloseMessage, notice, time.Now().Add(closeNoticeWait)); err != nil {
		h.logger.Printf("[Server %s] send going-away to client %q: %v", h.address, c.username, err)
	}
}

func (h *Hub) queueLoad(load int) {
	select {
	case h.loadUpdates <- load:
		return
	default:
	}

	select {
	case <-h.loadUpdates:
	default:
	}

	select {
	case h.loadUpdates <- load:
	default:
	}
}

func (h *Hub) reportLoads(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case load := <-h.loadUpdates:
			if err := h.reporter.UpdateLoad(ctx, load); err != nil && ctx.Err() == nil {
				h.logger.Printf("[Server %s] report load %d: %v", h.address, load, err)
			}
		}
	}
}

func (h *Hub) handleMessage(ctx context.Context, username, content string) error {
	message, err := h.saver.Save(ctx, Message{
		Username: username,
		Content:  content,
		Server:   h.address,
	})
	if err != nil {
		return fmt.Errorf("save message: %w", err)
	}

	messagesReceived.Add(ctx, 1)

	if err := h.broker.Publish(ctx, message); err != nil {
		return fmt.Errorf("publish message: %w", err)
	}

	return nil
}
