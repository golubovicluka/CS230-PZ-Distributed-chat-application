package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/golubovicluka/CS230-PZ-Distributed-chat-application/internal/chat"
)

func TestOpen(t *testing.T) {
	tests := []struct {
		name              string
		dataSourceName    func(*testing.T) string
		wantErrorContains string
		checkSchema       bool
	}{
		{
			name: "creates messages schema",
			dataSourceName: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "chat.db")
			},
			checkSchema: true,
		},
		{
			name: "reports connection failure",
			dataSourceName: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "missing", "chat.db")
			},
			wantErrorContains: "ping sqlite",
		},
		{
			name: "reports schema failure",
			dataSourceName: func(t *testing.T) string {
				path := filepath.Join(t.TempDir(), "read-only.db")
				if err := os.WriteFile(path, nil, 0o600); err != nil {
					t.Fatalf("create read-only database: %v", err)
				}
				return "file:" + path + "?mode=ro"
			},
			wantErrorContains: "create messages schema",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, err := Open(tt.dataSourceName(t))
			if tt.wantErrorContains != "" {
				if err == nil {
					if closeErr := store.Close(); closeErr != nil {
						t.Errorf("close unexpectedly opened store: %v", closeErr)
					}
					t.Fatalf("Open() error = nil, want error containing %q", tt.wantErrorContains)
				}
				if store != nil {
					t.Fatalf("Open() store = %#v, want nil after failure", store)
				}
				if !strings.Contains(err.Error(), tt.wantErrorContains) {
					t.Fatalf("Open() error = %q, want error containing %q", err, tt.wantErrorContains)
				}
				return
			}

			if err != nil {
				t.Fatalf("Open() error = %v", err)
			}
			t.Cleanup(func() {
				if err := store.Close(); err != nil {
					t.Errorf("Close() error = %v", err)
				}
			})

			if !tt.checkSchema {
				return
			}

			var schema string
			err = store.db.QueryRow(`
				SELECT sql
				FROM sqlite_master
				WHERE type = 'table' AND name = 'messages';
			`).Scan(&schema)
			if err != nil {
				t.Fatalf("query messages schema: %v", err)
			}

			for _, column := range []string{
				"id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT",
				"username TEXT NOT NULL",
				"message TEXT NOT NULL",
				"server TEXT NOT NULL",
				"timestamp DATETIME NOT NULL",
			} {
				if !strings.Contains(schema, column) {
					t.Errorf("messages schema %q does not contain %q", schema, column)
				}
			}

			var indexSQL string
			err = store.db.QueryRow(`
				SELECT sql
				FROM sqlite_master
				WHERE type = 'index' AND name = 'messages_recent_idx';
			`).Scan(&indexSQL)
			if err != nil {
				t.Fatalf("query recent messages index: %v", err)
			}
			if !strings.Contains(indexSQL, "timestamp DESC, id DESC") {
				t.Errorf("recent messages index = %q", indexSQL)
			}
		})
	}
}

func TestOpenExistingLegacySchema(t *testing.T) {
	tests := []struct {
		name      string
		legacyMsg chat.Message
	}{
		{
			name: "reads and writes message column",
			legacyMsg: chat.Message{
				Username:  "legacy-user",
				Content:   "legacy-message",
				Server:    "legacy-server",
				Timestamp: "2026-08-05 12:00:01",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "legacy.db")
			createLegacyDatabase(t, path, tt.legacyMsg)

			store, err := Open(path)
			if err != nil {
				t.Fatalf("Open() legacy database error = %v", err)
			}
			t.Cleanup(func() {
				if err := store.Close(); err != nil {
					t.Errorf("Close() legacy database error = %v", err)
				}
			})

			messages, err := store.Recent(context.Background(), 10)
			if err != nil {
				t.Fatalf("Recent() legacy database error = %v", err)
			}
			if len(messages) != 1 {
				t.Fatalf("Recent() legacy database length = %d, want 1", len(messages))
			}
			wantLegacy := tt.legacyMsg
			wantLegacy.ID = 1
			wantLegacy.Timestamp = "2026-08-05T12:00:01Z"
			if !reflect.DeepEqual(messages[0], wantLegacy) {
				t.Errorf("Recent() legacy message = %#v, want %#v", messages[0], wantLegacy)
			}

			stored, err := store.Save(context.Background(), chat.Message{
				Username: "new-user",
				Content:  "new-message",
				Server:   "new-server",
			})
			if err != nil {
				t.Fatalf("Save() legacy database error = %v", err)
			}
			if stored.ID != 2 {
				t.Errorf("Save() legacy database ID = %d, want 2", stored.ID)
			}
			if stored.Content != "new-message" {
				t.Errorf("Save() legacy database Content = %q, want %q", stored.Content, "new-message")
			}
		})
	}
}

func TestStoreSave(t *testing.T) {
	tests := []struct {
		name string
		msg  chat.Message
	}{
		{
			name: "persists every message field",
			msg: chat.Message{
				Username: "luka",
				Content:  "clear is better than clever",
				Server:   "chat-1",
			},
		},
		{
			name: "ignores caller supplied database fields",
			msg: chat.Message{
				ID:        99,
				Username:  "rob",
				Content:   "don't communicate by sharing memory",
				Server:    "chat-2",
				Timestamp: "caller-supplied",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := openTestStore(t)

			stored, err := store.Save(context.Background(), tt.msg)
			if err != nil {
				t.Fatalf("Save() error = %v", err)
			}

			if stored.ID != 1 {
				t.Errorf("Save() ID = %d, want 1", stored.ID)
			}
			if stored.Username != tt.msg.Username {
				t.Errorf("Save() Username = %q, want %q", stored.Username, tt.msg.Username)
			}
			if stored.Content != tt.msg.Content {
				t.Errorf("Save() Content = %q, want %q", stored.Content, tt.msg.Content)
			}
			if stored.Server != tt.msg.Server {
				t.Errorf("Save() Server = %q, want %q", stored.Server, tt.msg.Server)
			}
			if _, err := time.Parse(time.RFC3339Nano, stored.Timestamp); err != nil {
				t.Errorf("Save() Timestamp = %q, want normalized SQLite timestamp: %v", stored.Timestamp, err)
			}

			recent, err := store.Recent(context.Background(), 1)
			if err != nil {
				t.Fatalf("Recent() error = %v", err)
			}
			if len(recent) != 1 {
				t.Fatalf("Recent() length = %d, want 1", len(recent))
			}
			if !reflect.DeepEqual(recent[0], stored) {
				t.Errorf("Recent()[0] = %#v, want %#v", recent[0], stored)
			}
		})
	}
}

func TestStoreRecentEmpty(t *testing.T) {
	tests := []struct {
		name  string
		limit int
	}{
		{name: "one", limit: 1},
		{name: "many", limit: 50},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := openTestStore(t)

			messages, err := store.Recent(context.Background(), tt.limit)
			if err != nil {
				t.Fatalf("Recent() error = %v", err)
			}
			if messages == nil {
				t.Fatal("Recent() returned nil, want non-nil empty slice")
			}
			if len(messages) != 0 {
				t.Fatalf("Recent() length = %d, want 0", len(messages))
			}
		})
	}
}

func TestStoreRecentOrderingAndLimit(t *testing.T) {
	tests := []struct {
		name        string
		limit       int
		wantContent []string
	}{
		{name: "latest tie", limit: 2, wantContent: []string{"alpha", "charlie"}},
		{name: "latest three", limit: 3, wantContent: []string{"delta", "alpha", "charlie"}},
		{name: "more than stored", limit: 10, wantContent: []string{"bravo", "delta", "alpha", "charlie"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := openTestStore(t)
			contents := []string{"alpha", "bravo", "charlie", "delta"}
			timestamps := []string{
				"2026-08-05 12:00:03",
				"2026-08-05 12:00:01",
				"2026-08-05 12:00:03",
				"2026-08-05 12:00:02",
			}

			for i, content := range contents {
				stored, err := store.Save(context.Background(), chat.Message{
					Username: "user",
					Content:  content,
					Server:   "chat-1",
				})
				if err != nil {
					t.Fatalf("Save(%q) error = %v", content, err)
				}
				if _, err := store.db.Exec(
					"UPDATE messages SET timestamp = ? WHERE id = ?",
					timestamps[i],
					stored.ID,
				); err != nil {
					t.Fatalf("set timestamp for %q: %v", content, err)
				}
			}

			messages, err := store.Recent(context.Background(), tt.limit)
			if err != nil {
				t.Fatalf("Recent() error = %v", err)
			}

			gotContent := make([]string, 0, len(messages))
			for _, msg := range messages {
				gotContent = append(gotContent, msg.Content)
			}
			if !reflect.DeepEqual(gotContent, tt.wantContent) {
				t.Errorf("Recent() content = %#v, want %#v", gotContent, tt.wantContent)
			}
		})
	}
}

func TestStoreRecentRejectsInvalidLimit(t *testing.T) {
	tests := []struct {
		name  string
		limit int
	}{
		{name: "zero", limit: 0},
		{name: "negative", limit: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := openTestStore(t)

			messages, err := store.Recent(context.Background(), tt.limit)
			if err == nil {
				t.Fatal("Recent() error = nil, want invalid limit error")
			}
			if messages != nil {
				t.Errorf("Recent() messages = %#v, want nil after invalid limit", messages)
			}
			if !strings.Contains(err.Error(), "limit must be positive") {
				t.Errorf("Recent() error = %q, want invalid limit context", err)
			}
		})
	}
}

func TestStoreHonorsCanceledContext(t *testing.T) {
	tests := []struct {
		name string
		run  func(context.Context, *Store) error
	}{
		{
			name: "save",
			run: func(ctx context.Context, store *Store) error {
				_, err := store.Save(ctx, chat.Message{
					Username: "user",
					Content:  "message",
					Server:   "chat-1",
				})
				return err
			},
		},
		{
			name: "recent",
			run: func(ctx context.Context, store *Store) error {
				_, err := store.Recent(ctx, 1)
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := openTestStore(t)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			err := tt.run(ctx, store)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("operation error = %v, want context.Canceled", err)
			}
		})
	}
}

func TestStoreConcurrentWrites(t *testing.T) {
	tests := []struct {
		name   string
		writes int
	}{
		{name: "eight writers", writes: 8},
		{name: "thirty two writers", writes: 32},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := openTestStore(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			type result struct {
				message chat.Message
				err     error
			}
			results := make(chan result, tt.writes)

			for i := 0; i < tt.writes; i++ {
				go func(i int) {
					message, err := store.Save(ctx, chat.Message{
						Username: fmt.Sprintf("user-%d", i),
						Content:  fmt.Sprintf("message-%d", i),
						Server:   "chat-1",
					})
					results <- result{message: message, err: err}
				}(i)
			}

			ids := make(map[int64]struct{}, tt.writes)
			for i := 0; i < tt.writes; i++ {
				result := <-results
				if result.err != nil {
					t.Errorf("concurrent Save() error = %v", result.err)
					continue
				}
				if _, exists := ids[result.message.ID]; exists {
					t.Errorf("concurrent Save() returned duplicate ID %d", result.message.ID)
				}
				ids[result.message.ID] = struct{}{}
			}

			messages, err := store.Recent(ctx, tt.writes)
			if err != nil {
				t.Fatalf("Recent() after concurrent writes error = %v", err)
			}
			if len(messages) != tt.writes {
				t.Fatalf("Recent() length = %d, want %d", len(messages), tt.writes)
			}
		})
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()

	store, err := Open(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	return store
}

func createLegacyDatabase(t *testing.T, path string, msg chat.Message) {
	t.Helper()

	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}

	_, err = db.Exec(`
		CREATE TABLE messages (
			"id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
			"username" TEXT,
			"message" TEXT,
			"server" TEXT,
			"timestamp" DATETIME DEFAULT CURRENT_TIMESTAMP
		);
	`)
	if err != nil {
		if closeErr := db.Close(); closeErr != nil {
			t.Errorf("close legacy database after schema failure: %v", closeErr)
		}
		t.Fatalf("create legacy schema: %v", err)
	}

	_, err = db.Exec(
		"INSERT INTO messages (username, message, server, timestamp) VALUES (?, ?, ?, ?)",
		msg.Username,
		msg.Content,
		msg.Server,
		msg.Timestamp,
	)
	if err != nil {
		if closeErr := db.Close(); closeErr != nil {
			t.Errorf("close legacy database after insert failure: %v", closeErr)
		}
		t.Fatalf("insert legacy message: %v", err)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}
}
