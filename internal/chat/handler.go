package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/gorilla/websocket"
)

const (
	historyLimit      = 50
	maxUsernameLength = 64
)

// HistoryReader is the read-only persistence seam used by the HTTP handler.
type HistoryReader interface {
	Recent(context.Context, int) ([]Message, error)
}

// Handler owns the chat server's HTTP routes.
type Handler struct {
	hub      *Hub
	history  HistoryReader
	upgrader websocket.Upgrader
	logger   *log.Logger
}

func NewHandler(hub *Hub, history HistoryReader, checkOrigin func(*http.Request) bool, logger *log.Logger) *Handler {
	return &Handler{
		hub:     hub,
		history: history,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin:     checkOrigin,
		},
		logger: logger,
	}
}

func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /history", h.getHistory)
	mux.HandleFunc("GET /ws", h.serveWebSocket)
	return mux
}

func (h *Handler) getHistory(w http.ResponseWriter, r *http.Request) {
	messages, err := h.history.Recent(r.Context(), historyLimit)
	if err != nil {
		h.logger.Printf("retrieve message history: %v", err)
		http.Error(w, "failed to retrieve message history", http.StatusInternalServerError)
		return
	}
	if messages == nil {
		messages = []Message{}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(messages); err != nil {
		h.logger.Printf("write message history: %v", err)
	}
}

func (h *Handler) serveWebSocket(w http.ResponseWriter, r *http.Request) {
	username, err := validUsername(r.URL.Query().Get("username"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	connection, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Printf("upgrade WebSocket connection: %v", err)
		return
	}

	client := newClient(h.hub, connection, username)
	if err := h.hub.registerClient(r.Context(), client); err != nil {
		h.logger.Printf("register client %q: %v", username, err)
		if closeErr := connection.Close(); closeErr != nil {
			h.logger.Printf("close unregistered client %q: %v", username, closeErr)
		}
		return
	}

	go client.writePump()
	client.readPump(r.Context())
}

func validUsername(value string) (string, error) {
	username := strings.TrimSpace(value)
	if username == "" {
		return "", fmt.Errorf("username required")
	}
	if utf8.RuneCountInString(username) > maxUsernameLength {
		return "", fmt.Errorf("username must be at most %d characters", maxUsernameLength)
	}
	return username, nil
}
