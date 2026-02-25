package state

import (
	"database/sql"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type DB struct {
	conn *sql.DB
}

func Connect(dbPath string) (*DB, error) {
	conn, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}

	if err := migrate(conn); err != nil {
		return nil, err
	}

	return &DB{conn: conn}, nil
}

func migrate(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS sessions (
		id TEXT PRIMARY KEY,
		created_at DATETIME,
		working_dir TEXT,
		mode TEXT
	);
	CREATE TABLE IF NOT EXISTS messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id TEXT,
		role TEXT,
		agent_role TEXT,
		content TEXT,
		tokens_used INTEGER,
		created_at DATETIME
	);
	CREATE TABLE IF NOT EXISTS memory_blocks (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id TEXT,
		summary TEXT,
		created_at DATETIME
	);
	CREATE TABLE IF NOT EXISTS task_results (
		id TEXT PRIMARY KEY,
		session_id TEXT,
		agent_role TEXT,
		input TEXT,
		output TEXT,
		status TEXT,
		created_at DATETIME
	);`
	_, err := db.Exec(schema)
	return err
}

func (db *DB) Close() error {
	return db.conn.Close()
}

type Session struct {
	ID         string
	CreatedAt  time.Time
	WorkingDir string
	Mode       string
}
