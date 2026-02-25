package state

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
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
	executionMode := NormalizeExecutionMode("")
	if err := db.SetSessionExecutionMode(ctx, id, executionMode); err != nil {
		return nil, err
	}
	return &Session{ID: id, CreatedAt: now, WorkingDir: workingDir, Mode: mode, ExecutionMode: executionMode}, nil
}

func (db *DB) GetSession(ctx context.Context, id string) (*Session, error) {
	var s Session
	if err := db.conn.QueryRowContext(ctx,
		"SELECT id, created_at, working_dir, mode FROM sessions WHERE id = ?",
		id,
	).Scan(&s.ID, &s.CreatedAt, &s.WorkingDir, &s.Mode); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("session %q not found", id)
		}
		return nil, err
	}
	executionMode, err := db.GetSessionExecutionMode(ctx, s.ID)
	if err != nil {
		return nil, err
	}
	s.ExecutionMode = executionMode
	return &s, nil
}

func NormalizeExecutionMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case ExecutionModePlan:
		return ExecutionModePlan
	default:
		return ExecutionModeFast
	}
}

func (db *DB) SetSessionExecutionMode(ctx context.Context, sessionID, mode string) error {
	mode = NormalizeExecutionMode(mode)
	_, err := db.conn.ExecContext(ctx, `
		INSERT INTO session_settings (session_id, execution_mode, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			execution_mode = excluded.execution_mode,
			updated_at = excluded.updated_at
	`, strings.TrimSpace(sessionID), mode, time.Now().UTC())
	return err
}

func (db *DB) GetSessionExecutionMode(ctx context.Context, sessionID string) (string, error) {
	var mode string
	err := db.conn.QueryRowContext(ctx, `
		SELECT execution_mode
		FROM session_settings
		WHERE session_id = ?
	`, strings.TrimSpace(sessionID)).Scan(&mode)
	if errors.Is(err, sql.ErrNoRows) {
		return ExecutionModeFast, nil
	}
	if err != nil {
		return "", err
	}
	return NormalizeExecutionMode(mode), nil
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
