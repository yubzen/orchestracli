package state

import (
	"context"
	"strings"
	"time"
)

const DefaultInputHistoryLimit = 100

func (db *DB) AppendSessionInputHistory(ctx context.Context, sessionID, content string) error {
	sessionID = strings.TrimSpace(sessionID)
	content = strings.TrimSpace(content)
	if sessionID == "" || content == "" {
		return nil
	}

	if _, err := db.conn.ExecContext(ctx, `
		INSERT INTO session_input_history (session_id, content, created_at)
		VALUES (?, ?, ?)
	`, sessionID, content, time.Now().UTC()); err != nil {
		return err
	}

	_, err := db.conn.ExecContext(ctx, `
		DELETE FROM session_input_history
		WHERE session_id = ?
		  AND id NOT IN (
			SELECT id
			FROM session_input_history
			WHERE session_id = ?
			ORDER BY id DESC
			LIMIT ?
		  )
	`, sessionID, sessionID, DefaultInputHistoryLimit)
	return err
}

func (db *DB) GetSessionInputHistory(ctx context.Context, sessionID string) ([]string, error) {
	rows, err := db.conn.QueryContext(ctx, `
		SELECT content
		FROM session_input_history
		WHERE session_id = ?
		ORDER BY id ASC
	`, strings.TrimSpace(sessionID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]string, 0, DefaultInputHistoryLimit)
	for rows.Next() {
		var content string
		if err := rows.Scan(&content); err != nil {
			return nil, err
		}
		content = strings.TrimSpace(content)
		if content == "" {
			continue
		}
		out = append(out, content)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
