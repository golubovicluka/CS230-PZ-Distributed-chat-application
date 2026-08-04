package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/golubovicluka/CS230-PZ-Distributed-chat-application/internal/chat"
	_ "github.com/mattn/go-sqlite3"
)

const createMessagesTable = `
CREATE TABLE IF NOT EXISTS messages (
	id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
	username TEXT NOT NULL,
	message TEXT NOT NULL,
	server TEXT NOT NULL,
	timestamp DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);`

const createRecentMessagesIndex = `
CREATE INDEX IF NOT EXISTS messages_recent_idx
ON messages (timestamp DESC, id DESC);`

const insertMessage = `
INSERT INTO messages (username, message, server)
VALUES (?, ?, ?)
RETURNING id, timestamp;`

const selectRecentMessages = `
SELECT id, username, message, server, timestamp
FROM (
	SELECT id, username, message, server, timestamp
	FROM messages
	ORDER BY timestamp DESC, id DESC
	LIMIT ?
)
ORDER BY timestamp ASC, id ASC;`

// Store persists chat messages in one SQLite database.
type Store struct {
	db *sql.DB
}

// Open opens the exact data source name supplied by the caller and prepares
// the messages schema.
func Open(dataSourceName string) (*Store, error) {
	db, err := sql.Open("sqlite3", dataSourceName)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// A single connection keeps in-memory data sources coherent and lets
	// SQLite serialize writes without a process-level mutex.
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		pingErr := fmt.Errorf("ping sqlite: %w", err)
		if closeErr := db.Close(); closeErr != nil {
			return nil, errors.Join(pingErr, fmt.Errorf("close sqlite after ping failure: %w", closeErr))
		}
		return nil, pingErr
	}

	if _, err := db.Exec(createMessagesTable); err != nil {
		schemaErr := fmt.Errorf("create messages schema: %w", err)
		if closeErr := db.Close(); closeErr != nil {
			return nil, errors.Join(schemaErr, fmt.Errorf("close sqlite after schema failure: %w", closeErr))
		}
		return nil, schemaErr
	}
	if _, err := db.Exec(createRecentMessagesIndex); err != nil {
		indexErr := fmt.Errorf("create recent messages index: %w", err)
		if closeErr := db.Close(); closeErr != nil {
			return nil, errors.Join(indexErr, fmt.Errorf("close sqlite after index failure: %w", closeErr))
		}
		return nil, indexErr
	}

	return &Store{db: db}, nil
}

// Save persists msg and returns the values assigned by SQLite.
func (s *Store) Save(ctx context.Context, msg chat.Message) (chat.Message, error) {
	stored := chat.Message{
		Username: msg.Username,
		Content:  msg.Content,
		Server:   msg.Server,
	}

	err := s.db.QueryRowContext(
		ctx,
		insertMessage,
		stored.Username,
		stored.Content,
		stored.Server,
	).Scan(&stored.ID, &stored.Timestamp)
	if err != nil {
		return chat.Message{}, fmt.Errorf("save message: %w", err)
	}

	return stored, nil
}

// Recent returns the latest limit messages in chronological order.
func (s *Store) Recent(ctx context.Context, limit int) ([]chat.Message, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("recent messages: limit must be positive: %d", limit)
	}

	rows, err := s.db.QueryContext(ctx, selectRecentMessages, limit)
	if err != nil {
		return nil, fmt.Errorf("query recent messages: %w", err)
	}

	messages := make([]chat.Message, 0)
	for rows.Next() {
		var msg chat.Message
		if err := rows.Scan(&msg.ID, &msg.Username, &msg.Content, &msg.Server, &msg.Timestamp); err != nil {
			scanErr := fmt.Errorf("scan recent message: %w", err)
			if closeErr := rows.Close(); closeErr != nil {
				return nil, errors.Join(scanErr, fmt.Errorf("close recent message rows after scan failure: %w", closeErr))
			}
			return nil, scanErr
		}
		messages = append(messages, msg)
	}

	if err := rows.Err(); err != nil {
		iterationErr := fmt.Errorf("iterate recent messages: %w", err)
		if closeErr := rows.Close(); closeErr != nil {
			return nil, errors.Join(iterationErr, fmt.Errorf("close recent message rows after iteration failure: %w", closeErr))
		}
		return nil, iterationErr
	}

	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close recent message rows: %w", err)
	}

	return messages, nil
}

// Close releases the database resources owned by the store.
func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("close sqlite store: %w", err)
	}
	return nil
}
