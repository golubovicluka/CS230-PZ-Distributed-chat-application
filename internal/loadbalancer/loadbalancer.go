// Package loadbalancer selects the least-loaded registered chat server.
package loadbalancer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
)

const maxRequestBodyBytes int64 = 64 << 10

var (
	errAddressRequired = errors.New("address is required")
	errLoadNegative    = errors.New("load must not be negative")
)

// serverRequest is the wire format accepted by the mutation endpoints.
type serverRequest struct {
	Address string `json:"address"`
	Load    int    `json:"load"`
}

// serverResponse is the legacy wire format returned by the selection endpoint.
type serverResponse struct {
	Address string `json:"Address"`
	Load    int    `json:"load"`
}

type serverInfo struct {
	address string
	load    int
}

// LoadBalancer stores the latest reported load for each chat server.
type LoadBalancer struct {
	mu      sync.RWMutex
	servers map[string]int
	logger  *log.Logger
}

// New constructs an empty LoadBalancer. Logger must not be nil.
func New(logger *log.Logger) *LoadBalancer {
	if logger == nil {
		panic("loadbalancer: logger must not be nil")
	}

	return &LoadBalancer{
		servers: make(map[string]int),
		logger:  logger,
	}
}

// Handler returns the load balancer's HTTP routes.
func (lb *LoadBalancer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /register", lb.handleRegister)
	mux.HandleFunc("POST /update", lb.handleUpdate)
	mux.HandleFunc("GET /get", lb.handleGet)
	return mux
}

func (lb *LoadBalancer) handleRegister(w http.ResponseWriter, r *http.Request) {
	server, ok := lb.readServer(w, r)
	if !ok {
		return
	}

	lb.upsert(server)
	lb.logger.Printf("registered server %s with load %d", server.address, server.load)
	w.WriteHeader(http.StatusOK)
}

func (lb *LoadBalancer) handleUpdate(w http.ResponseWriter, r *http.Request) {
	server, ok := lb.readServer(w, r)
	if !ok {
		return
	}

	lb.upsert(server)
	lb.logger.Printf("updated server %s to load %d", server.address, server.load)
	w.WriteHeader(http.StatusOK)
}

func (lb *LoadBalancer) handleGet(w http.ResponseWriter, _ *http.Request) {
	server, ok := lb.leastLoaded()
	if !ok {
		http.Error(w, "no available servers", http.StatusServiceUnavailable)
		return
	}

	lb.logger.Printf("selected server %s with load %d", server.address, server.load)
	response := serverResponse{Address: server.address, Load: server.load}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		lb.logger.Printf("write selected server response: %v", err)
	}
}

func (lb *LoadBalancer) readServer(w http.ResponseWriter, r *http.Request) (serverInfo, bool) {
	server, err := decodeServer(w, r)
	if err == nil {
		return server, true
	}

	status, message := requestErrorResponse(err)
	lb.logger.Printf("reject %s %s: %v", r.Method, r.URL.Path, err)
	http.Error(w, message, status)
	return serverInfo{}, false
}

func decodeServer(w http.ResponseWriter, r *http.Request) (serverInfo, error) {
	if r.Body == nil {
		return serverInfo{}, errors.New("request body is required")
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	var request serverRequest
	if err := decoder.Decode(&request); err != nil {
		return serverInfo{}, fmt.Errorf("decode request body: %w", err)
	}

	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return serverInfo{}, errors.New("request body must contain exactly one JSON object")
		}
		return serverInfo{}, fmt.Errorf("decode trailing request data: %w", err)
	}

	address := strings.TrimSpace(request.Address)
	if address == "" {
		return serverInfo{}, errAddressRequired
	}
	if request.Load < 0 {
		return serverInfo{}, errLoadNegative
	}

	return serverInfo{address: address, load: request.Load}, nil
}

func requestErrorResponse(err error) (int, string) {
	var maxBytesError *http.MaxBytesError
	switch {
	case errors.As(err, &maxBytesError):
		return http.StatusRequestEntityTooLarge, "request body is too large"
	case errors.Is(err, errAddressRequired):
		return http.StatusBadRequest, errAddressRequired.Error()
	case errors.Is(err, errLoadNegative):
		return http.StatusBadRequest, errLoadNegative.Error()
	default:
		return http.StatusBadRequest, "invalid request body"
	}
}

func (lb *LoadBalancer) upsert(server serverInfo) {
	lb.mu.Lock()
	lb.servers[server.address] = server.load
	lb.mu.Unlock()
}

func (lb *LoadBalancer) leastLoaded() (serverInfo, bool) {
	lb.mu.RLock()
	defer lb.mu.RUnlock()

	var best serverInfo
	found := false
	for address, load := range lb.servers {
		server := serverInfo{address: address, load: load}
		if !found || server.load < best.load || (server.load == best.load && server.address < best.address) {
			best = server
			found = true
		}
	}

	return best, found
}
