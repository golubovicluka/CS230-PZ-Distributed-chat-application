// Package postgres persists chat messages in PostgreSQL.
package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"github.com/golubovicluka/CS230-PZ-Distributed-chat-application/internal/chat"
)

//go:embed migrations/*.sql
var migrations embed.FS

const insertMessage = `
INSERT INTO messages (username, content, server)
VALUES ($1, $2, $3)
RETURNING id, created_at;`

const selectRecentMessages = `
SELECT id, username, content, server, created_at
FROM (
	SELECT id, username, content, server, created_at
	FROM messages
	ORDER BY created_at DESC, id DESC
	LIMIT $1
) recent
ORDER BY created_at ASC, id ASC;`

// Store persists chat messages in one PostgreSQL database shared by every
// chat server, so history is identical regardless of which server a client
// joins.
type Store struct {
	pool *pgxpool.Pool
}

// Open connects with the supplied pool configuration, applies pending schema
// migrations under an advisory lock, and returns a ready store. Callers own
// the configuration so they can attach tracers or session parameters.
func Open(ctx context.Context, config *pgxpool.Config) (*Store, error) {
	if config == nil {
		return nil, errors.New("postgres: pool configuration is required")
	}

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("create postgres pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	if err := migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}

	return &Store{pool: pool}, nil
}

func migrate(ctx context.Context, pool *pgxpool.Pool) error {
	files, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("load embedded migrations: %w", err)
	}
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return fmt.Errorf("create migration locker: %w", err)
	}

	db := stdlib.OpenDBFromPool(pool)
	provider, err := goose.NewProvider(goose.DialectPostgres, db, files, goose.WithSessionLocker(locker))
	if err != nil {
		return errors.Join(fmt.Errorf("create migration provider: %w", err), db.Close())
	}
	if _, err := provider.Up(ctx); err != nil {
		return errors.Join(fmt.Errorf("apply migrations: %w", err), db.Close())
	}
	if err := db.Close(); err != nil {
		return fmt.Errorf("close migration connection: %w", err)
	}
	return nil
}

// Save persists msg and returns the values assigned by PostgreSQL.
func (s *Store) Save(ctx context.Context, msg chat.Message) (chat.Message, error) {
	stored := chat.Message{
		Username: msg.Username,
		Content:  msg.Content,
		Server:   msg.Server,
	}

	var createdAt time.Time
	err := s.pool.QueryRow(ctx, insertMessage, stored.Username, stored.Content, stored.Server).
		Scan(&stored.ID, &createdAt)
	if err != nil {
		return chat.Message{}, fmt.Errorf("save message: %w", err)
	}
	stored.Timestamp = formatTimestamp(createdAt)

	return stored, nil
}

// Recent returns the latest limit messages in chronological order.
func (s *Store) Recent(ctx context.Context, limit int) ([]chat.Message, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("recent messages: limit must be positive: %d", limit)
	}

	rows, err := s.pool.Query(ctx, selectRecentMessages, limit)
	if err != nil {
		return nil, fmt.Errorf("query recent messages: %w", err)
	}
	defer rows.Close()

	messages := make([]chat.Message, 0)
	for rows.Next() {
		var msg chat.Message
		var createdAt time.Time
		if err := rows.Scan(&msg.ID, &msg.Username, &msg.Content, &msg.Server, &createdAt); err != nil {
			return nil, fmt.Errorf("scan recent message: %w", err)
		}
		msg.Timestamp = formatTimestamp(createdAt)
		messages = append(messages, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recent messages: %w", err)
	}

	return messages, nil
}

// Ping reports whether the database is reachable.
func (s *Store) Ping(ctx context.Context) error {
	if err := s.pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping postgres: %w", err)
	}
	return nil
}

// Close releases every pooled connection.
func (s *Store) Close() {
	s.pool.Close()
}

func formatTimestamp(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}
