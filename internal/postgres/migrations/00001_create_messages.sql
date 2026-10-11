-- +goose Up
CREATE TABLE messages (
	id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
	username TEXT NOT NULL,
	content TEXT NOT NULL,
	server TEXT NOT NULL,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX messages_recent_idx ON messages (created_at DESC, id DESC);

-- +goose Down
DROP TABLE messages;
