package chat

import "testing"

func TestDecodeIncomingMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		payload     string
		wantContent string
		wantErr     bool
	}{
		{name: "valid frontend payload", payload: `{"username":"ignored","content":"hello"}`, wantContent: "hello"},
		{name: "valid content without username", payload: `{"content":"zdravo"}`, wantContent: "zdravo"},
		{name: "rejects malformed JSON", payload: `{"content":`, wantErr: true},
		{name: "rejects unknown fields", payload: `{"content":"hello","extra":true}`, wantErr: true},
		{name: "rejects trailing JSON", payload: `{"content":"hello"} {"content":"again"}`, wantErr: true},
		{name: "rejects missing content", payload: `{"username":"alice"}`, wantErr: true},
		{name: "rejects blank content", payload: `{"content":"   "}`, wantErr: true},
		{name: "rejects null", payload: `null`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			message, err := decodeIncomingMessage([]byte(tt.payload))
			if (err != nil) != tt.wantErr {
				t.Fatalf("decodeIncomingMessage() error = %v, wantErr %t", err, tt.wantErr)
			}
			if message.Content != tt.wantContent {
				t.Errorf("content = %q, want %q", message.Content, tt.wantContent)
			}
		})
	}
}
