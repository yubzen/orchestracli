package state

import (
	"context"
	"strings"
	"time"
)

type SessionModelSelection struct {
	SessionID   string
	Role        string
	ProviderKey string
	ModelID     string
	UpdatedAt   time.Time
}

func (db *DB) SaveSessionModelSelection(ctx context.Context, sessionID, role, providerKey, modelID string) error {
	sessionID = strings.TrimSpace(sessionID)
	role = strings.ToUpper(strings.TrimSpace(role))
	providerKey = strings.TrimSpace(providerKey)
	modelID = strings.TrimSpace(modelID)
	_, err := db.conn.ExecContext(ctx, `
		INSERT INTO session_model_selections (session_id, role, provider_key, model_id, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(session_id, role) DO UPDATE SET
			provider_key = excluded.provider_key,
			model_id = excluded.model_id,
			updated_at = excluded.updated_at
	`, sessionID, role, providerKey, modelID, time.Now().UTC())
	return err
}

func (db *DB) GetSessionModelSelections(ctx context.Context, sessionID string) (map[string]SessionModelSelection, error) {
	rows, err := db.conn.QueryContext(ctx, `
		SELECT session_id, role, provider_key, model_id, updated_at
		FROM session_model_selections
		WHERE session_id = ?
		ORDER BY role ASC
	`, strings.TrimSpace(sessionID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]SessionModelSelection)
	for rows.Next() {
		var entry SessionModelSelection
		if err := rows.Scan(&entry.SessionID, &entry.Role, &entry.ProviderKey, &entry.ModelID, &entry.UpdatedAt); err != nil {
			return nil, err
		}
		out[strings.ToUpper(strings.TrimSpace(entry.Role))] = entry
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
