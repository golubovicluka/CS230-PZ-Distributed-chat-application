package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/golubovicluka/CS230-PZ-Distributed-chat-application/internal/chat"
)

func TestOpenRequiresConfig(t *testing.T) {
	t.Parallel()

	if _, err := Open(context.Background(), nil); err == nil {
		t.Fatal("Open(nil) error = nil, want configuration error")
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	config := testConfig(t)

	for attempt := 1; attempt <= 2; attempt++ {
		store, err := Open(context.Background(), config.Copy())
		if err != nil {
			t.Fatalf("Open() attempt %d error = %v", attempt, err)
		}
		store.Close()
	}
}

func TestStoreSave(t *testing.T) {
	tests := []struct {
		name string
		msg  chat.Message
	}{
		{
			name: "persists every message field",
			msg:  chat.Message{Username: "luka", Content: "clear is better than clever", Server: "chat-1"},
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
			if stored.Username != tt.msg.Username || stored.Content != tt.msg.Content || stored.Server != tt.msg.Server {
				t.Errorf("Save() = %+v, want fields from %+v", stored, tt.msg)
			}
			if _, err := time.Parse(time.RFC3339Nano, stored.Timestamp); err != nil {
				t.Errorf("Save() Timestamp = %q, want RFC 3339: %v", stored.Timestamp, err)
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
	store := openTestStore(t)

	messages, err := store.Recent(context.Background(), 50)
	if err != nil {
		t.Fatalf("Recent() error = %v", err)
	}
	if messages == nil {
		t.Fatal("Recent() returned nil, want non-nil empty slice")
	}
	if len(messages) != 0 {
		t.Fatalf("Recent() length = %d, want 0", len(messages))
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
				"2026-08-05T12:00:03Z",
				"2026-08-05T12:00:01Z",
				"2026-08-05T12:00:03Z",
				"2026-08-05T12:00:02Z",
			}

			for i, content := range contents {
				stored, err := store.Save(context.Background(), chat.Message{Username: "user", Content: content, Server: "chat-1"})
				if err != nil {
					t.Fatalf("Save(%q) error = %v", content, err)
				}
				if _, err := store.pool.Exec(
					context.Background(),
					"UPDATE messages SET created_at = $1 WHERE id = $2",
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
	store := openTestStore(t)

	for _, limit := range []int{0, -1} {
		messages, err := store.Recent(context.Background(), limit)
		if err == nil || !strings.Contains(err.Error(), "limit must be positive") {
			t.Fatalf("Recent(%d) error = %v, want invalid limit error", limit, err)
		}
		if messages != nil {
			t.Errorf("Recent(%d) messages = %#v, want nil", limit, messages)
		}
	}
}

func TestStoreHonorsCanceledContext(t *testing.T) {
	store := openTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := store.Save(ctx, chat.Message{Username: "user", Content: "message", Server: "chat-1"}); !errors.Is(err, context.Canceled) {
		t.Errorf("Save() error = %v, want context.Canceled", err)
	}
	if _, err := store.Recent(ctx, 1); !errors.Is(err, context.Canceled) {
		t.Errorf("Recent() error = %v, want context.Canceled", err)
	}
	if err := store.Ping(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Ping() error = %v, want context.Canceled", err)
	}
}

func TestStoreConcurrentWritesFromSeparateServers(t *testing.T) {
	config := testConfig(t)
	stores := make([]*Store, 2)
	for i := range stores {
		store, err := Open(context.Background(), config.Copy())
		if err != nil {
			t.Fatalf("Open() server %d error = %v", i, err)
		}
		t.Cleanup(store.Close)
		stores[i] = store
	}

	const writes = 32
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	type result struct {
		message chat.Message
		err     error
	}
	results := make(chan result, writes)
	for i := range writes {
		go func() {
			store := stores[i%len(stores)]
			message, err := store.Save(ctx, chat.Message{
				Username: fmt.Sprintf("user-%d", i),
				Content:  fmt.Sprintf("message-%d", i),
				Server:   fmt.Sprintf("chat-%d", i%len(stores)),
			})
			results <- result{message: message, err: err}
		}()
	}

	ids := make(map[int64]struct{}, writes)
	for range writes {
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

	for i, store := range stores {
		messages, err := store.Recent(ctx, writes)
		if err != nil {
			t.Fatalf("Recent() on server %d error = %v", i, err)
		}
		if len(messages) != writes {
			t.Errorf("Recent() on server %d length = %d, want %d shared messages", i, len(messages), writes)
		}
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()

	store, err := Open(context.Background(), testConfig(t))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(store.Close)
	return store
}

// testConfig returns a pool configuration bound to a fresh schema so tests
// stay isolated while sharing one database.
func testConfig(t *testing.T) *pgxpool.Config {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set; skipping PostgreSQL integration test")
	}

	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}

	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("generate schema name: %v", err)
	}
	schema := "test_" + hex.EncodeToString(suffix)
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create test schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("drop test schema: %v", err)
		}
		if err := admin.Close(context.Background()); err != nil {
			t.Errorf("close admin connection: %v", err)
		}
	})

	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	return config
}
