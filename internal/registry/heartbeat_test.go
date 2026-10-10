package registry

import (
	"context"
	"errors"
	"io"
	"log"
	"sync"
	"testing"
	"time"
)

type recordingRegistrar struct {
	mu           sync.Mutex
	loads        []int
	err          error
	calls        chan int
	deregistered chan struct{}
}

func (r *recordingRegistrar) UpdateLoad(_ context.Context, load int) error {
	r.mu.Lock()
	r.loads = append(r.loads, load)
	r.mu.Unlock()
	r.calls <- load
	return r.err
}

func (r *recordingRegistrar) Deregister(context.Context) error {
	close(r.deregistered)
	return nil
}

func TestNewHeartbeatValidation(t *testing.T) {
	t.Parallel()

	registrar := &recordingRegistrar{calls: make(chan int, 1)}
	logger := log.New(io.Discard, "", 0)
	tests := []struct {
		name      string
		registrar Registrar
		interval  time.Duration
		logger    *log.Logger
		wantErr   bool
	}{
		{name: "valid", registrar: registrar, interval: time.Second, logger: logger},
		{name: "missing registrar", interval: time.Second, logger: logger, wantErr: true},
		{name: "zero interval", registrar: registrar, logger: logger, wantErr: true},
		{name: "missing logger", registrar: registrar, interval: time.Second, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewHeartbeat(tt.registrar, tt.interval, tt.logger); (err != nil) != tt.wantErr {
				t.Fatalf("NewHeartbeat() error = %v, wantErr %t", err, tt.wantErr)
			}
		})
	}
}

func TestHeartbeatRepeatsLatestLoad(t *testing.T) {
	t.Parallel()

	registrar := &recordingRegistrar{calls: make(chan int, 16)}
	heartbeat, err := NewHeartbeat(registrar, 10*time.Millisecond, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatalf("NewHeartbeat() error = %v", err)
	}

	if err := heartbeat.UpdateLoad(context.Background(), 3); err != nil {
		t.Fatalf("UpdateLoad() error = %v", err)
	}
	if got := <-registrar.calls; got != 3 {
		t.Fatalf("immediate report = %d, want 3", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		heartbeat.Run(ctx)
		close(done)
	}()

	for range 2 {
		select {
		case got := <-registrar.calls:
			if got != 3 {
				t.Fatalf("heartbeat report = %d, want latest load 3", got)
			}
		case <-time.After(time.Second):
			t.Fatal("heartbeat did not repeat the latest load")
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run() did not stop after cancellation")
	}
}

func TestHeartbeatReturnsImmediateReportError(t *testing.T) {
	t.Parallel()

	failure := errors.New("registry unavailable")
	registrar := &recordingRegistrar{calls: make(chan int, 1), err: failure}
	heartbeat, err := NewHeartbeat(registrar, time.Hour, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatalf("NewHeartbeat() error = %v", err)
	}

	if err := heartbeat.UpdateLoad(context.Background(), 1); !errors.Is(err, failure) {
		t.Fatalf("UpdateLoad() error = %v, want %v", err, failure)
	}
}

func TestHeartbeatStopsReportingAfterDeregister(t *testing.T) {
	t.Parallel()

	registrar := &recordingRegistrar{calls: make(chan int, 16), deregistered: make(chan struct{})}
	heartbeat, err := NewHeartbeat(registrar, 10*time.Millisecond, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatalf("NewHeartbeat() error = %v", err)
	}

	done := make(chan struct{})
	go func() {
		heartbeat.Run(context.Background())
		close(done)
	}()

	if err := heartbeat.Deregister(context.Background()); err != nil {
		t.Fatalf("Deregister() error = %v", err)
	}
	<-registrar.deregistered

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run() kept going after Deregister")
	}

	for len(registrar.calls) > 0 {
		<-registrar.calls
	}
	if err := heartbeat.UpdateLoad(context.Background(), 4); err != nil {
		t.Fatalf("UpdateLoad() after Deregister error = %v", err)
	}
	if len(registrar.calls) != 0 {
		t.Fatal("UpdateLoad() reported a load after Deregister")
	}
}
