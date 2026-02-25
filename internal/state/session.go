package state

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"
)

type Message struct {
	ID         int64
	SessionID  string
	Role       string
	AgentRole  string
	Content    string
	TokensUsed int
	CreatedAt  time.Time
}

func genID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (db *DB) CreateSession(ctx context.Context, workingDir, mode string) (*Session, error) {
	id := genID()
	now := time.Now()
	_, err := db.conn.ExecContext(ctx, "INSERT INTO sessions (id, created_at, working_dir, mode) VALUES (?, ?, ?, ?)", id, now, workingDir, mode)
	if err != nil {
		return nil, err
	}
	return &Session{ID: id, CreatedAt: now, WorkingDir: workingDir, Mode: mode}, nil
}

func (db *DB) SaveMessage(ctx context.Context, sessionID, role, agentRole, content string, tokens int) error {
	_, err := db.conn.ExecContext(ctx, "INSERT INTO messages (session_id, role, agent_role, content, tokens_used, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		sessionID, role, agentRole, content, tokens, time.Now())
	return err
}

func (db *DB) GetMessages(ctx context.Context, sessionID string) ([]Message, error) {
	rows, err := db.conn.QueryContext(ctx, "SELECT id, session_id, role, agent_role, content, tokens_used, created_at FROM messages WHERE session_id = ? ORDER BY created_at ASC", sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var msgs []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Role, &m.AgentRole, &m.Content, &m.TokensUsed, &m.CreatedAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, nil
}
