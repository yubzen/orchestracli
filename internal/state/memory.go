package state

import (
	"context"
	"time"
)

type TaskResult struct {
	ID        string
	SessionID string
	AgentRole string
	Input     string
	Output    string
	Status    string
	CreatedAt time.Time
}

func (db *DB) SaveTaskResult(ctx context.Context, tr TaskResult) error {
	_, err := db.conn.ExecContext(ctx, "INSERT INTO task_results (id, session_id, agent_role, input, output, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		tr.ID, tr.SessionID, tr.AgentRole, tr.Input, tr.Output, tr.Status, tr.CreatedAt)
	return err
}

func (db *DB) SaveMemoryBlock(ctx context.Context, sessionID, summary string) error {
	_, err := db.conn.ExecContext(ctx, "INSERT INTO memory_blocks (session_id, summary, created_at) VALUES (?, ?, ?)",
		sessionID, summary, time.Now())
	return err
}
