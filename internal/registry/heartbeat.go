package registry

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"
)

// Registrar is the registry API used by the heartbeat.
type Registrar interface {
	UpdateLoad(context.Context, int) error
	Deregister(context.Context) error
}

// Heartbeat forwards load changes immediately and repeats the latest load on
// a fixed interval. The registry expires servers that stop reporting, so the
// repetition is what keeps a healthy but idle server selectable. Reports are
// serialized, so a heartbeat can never overwrite a newer load with an older
// one, and nothing is reported after Deregister, so a draining server cannot
// re-register itself.
type Heartbeat struct {
	registrar Registrar
	interval  time.Duration
	logger    *log.Logger

	mu      sync.Mutex
	load    int
	stopped bool
}

// NewHeartbeat validates its collaborators and returns an idle heartbeat.
func NewHeartbeat(registrar Registrar, interval time.Duration, logger *log.Logger) (*Heartbeat, error) {
	if registrar == nil {
		return nil, errors.New("registry heartbeat: registrar is required")
	}
	if interval <= 0 {
		return nil, errors.New("registry heartbeat: interval must be positive")
	}
	if logger == nil {
		return nil, errors.New("registry heartbeat: logger is required")
	}
	return &Heartbeat{registrar: registrar, interval: interval, logger: logger}, nil
}

// UpdateLoad records load as the latest value and reports it immediately.
func (h *Heartbeat) UpdateLoad(ctx context.Context, load int) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.load = load
	if h.stopped {
		return nil
	}
	return h.registrar.UpdateLoad(ctx, load)
}

// Deregister stops all further reports and removes the server from the
// registry.
func (h *Heartbeat) Deregister(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.stopped = true
	return h.registrar.Deregister(ctx)
}

// Run repeats the latest load every interval until ctx ends.
func (h *Heartbeat) Run(ctx context.Context) {
	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.mu.Lock()
			if h.stopped {
				h.mu.Unlock()
				return
			}
			err := h.registrar.UpdateLoad(ctx, h.load)
			load := h.load
			h.mu.Unlock()
			if err != nil && ctx.Err() == nil {
				h.logger.Printf("heartbeat load %d: %v", load, err)
			}
		}
	}
}
