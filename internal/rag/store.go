package rag

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	_ "github.com/mattn/go-sqlite3"
)

type Store struct {
	db *sql.DB

	mu         sync.RWMutex
	vecEnabled bool
}

var ErrStoreNotReady = errors.New("rag store is not initialized")

const (
	defaultSearchLimit       = 5
	defaultDistanceThreshold = 0.85
	sqliteVecDimensions      = 768
)

var sqliteVecAutoOnce sync.Once

type Chunk struct {
	ID         string
	Filepath   string
	ChunkIndex int
	Content    string
}

func enableSQLiteVec() {
	sqliteVecAutoOnce.Do(func() {
		// Registers sqlite-vec as an auto-extension for new sqlite3 connections.
		sqlite_vec.Auto()
	})
}

func (s *Store) isVecEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.vecEnabled
}

func (s *Store) setVecEnabled(enabled bool) {
	s.mu.Lock()
	s.vecEnabled = enabled
	s.mu.Unlock()
}

func NewStore(dbPath string) (*Store, error) {
	enableSQLiteVec()

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open rag store %q: %w", dbPath, err)
	}

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect rag store %q: %w", dbPath, err)
	}

	schema := `
	CREATE TABLE IF NOT EXISTS file_chunks (
		rowid INTEGER PRIMARY KEY AUTOINCREMENT,
		id TEXT UNIQUE,
		filepath TEXT NOT NULL,
		chunk_index INTEGER NOT NULL,
		content TEXT NOT NULL,
		embedding TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_file_chunks_filepath ON file_chunks(filepath);
	`
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize rag schema: %w", err)
	}

	store := &Store{db: db}

	// Prefer sqlite-vec when available; keep fallback path if vec initialization fails.
	vecSchema := fmt.Sprintf("CREATE VIRTUAL TABLE IF NOT EXISTS vec_chunks USING vec0(embedding float[%d]);", sqliteVecDimensions)
	if _, err := db.Exec(vecSchema); err == nil {
		store.setVecEnabled(true)
	}

	return store, nil
}

func normalizeContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func (s *Store) EnsureReady(ctx context.Context) error {
	ctx = normalizeContext(ctx)

	if s == nil || s.db == nil {
		return ErrStoreNotReady
	}

	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("rag store connection is not ready: %w", err)
	}

	return nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) SaveChunk(ctx context.Context, chunk Chunk, embedding []float32) error {
	ctx = normalizeContext(ctx)

	if err := s.EnsureReady(ctx); err != nil {
		return err
	}
	if strings.TrimSpace(chunk.ID) == "" {
		return errors.New("chunk id is required")
	}
	if len(embedding) == 0 {
		return errors.New("embedding is required")
	}

	embJSON, err := json.Marshal(embedding)
	if err != nil {
		return fmt.Errorf("marshal embedding: %w", err)
	}

	if s.isVecEnabled() && len(embedding) == sqliteVecDimensions {
		if err := s.saveChunkWithVec(ctx, chunk, embedding, string(embJSON)); err == nil {
			return nil
		}
		s.setVecEnabled(false)
	}

	_, err = s.db.ExecContext(ctx,
		"INSERT OR REPLACE INTO file_chunks (id, filepath, chunk_index, content, embedding) VALUES (?, ?, ?, ?, ?)",
		chunk.ID, chunk.Filepath, chunk.ChunkIndex, chunk.Content, string(embJSON),
	)
	if err != nil {
		return err
	}
	return nil
}

func (s *Store) saveChunkWithVec(ctx context.Context, chunk Chunk, embedding []float32, embJSON string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var existingRowID sql.NullInt64
	_ = tx.QueryRowContext(ctx, "SELECT rowid FROM file_chunks WHERE id = ?", chunk.ID).Scan(&existingRowID)
	if existingRowID.Valid {
		if _, err := tx.ExecContext(ctx, "DELETE FROM vec_chunks WHERE rowid = ?", existingRowID.Int64); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM file_chunks WHERE rowid = ?", existingRowID.Int64); err != nil {
			return err
		}
	}

	res, err := tx.ExecContext(ctx,
		"INSERT INTO file_chunks (id, filepath, chunk_index, content, embedding) VALUES (?, ?, ?, ?, ?)",
		chunk.ID, chunk.Filepath, chunk.ChunkIndex, chunk.Content, embJSON,
	)
	if err != nil {
		return err
	}

	rowid, err := res.LastInsertId()
	if err != nil {
		return err
	}

	vecBlob, err := sqlite_vec.SerializeFloat32(embedding)
	if err != nil {
		return fmt.Errorf("serialize vector: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO vec_chunks (rowid, embedding) VALUES (?, ?)", rowid, vecBlob); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *Store) ClearFile(ctx context.Context, filepath string) error {
	ctx = normalizeContext(ctx)

	if err := s.EnsureReady(ctx); err != nil {
		return err
	}

	if s.isVecEnabled() {
		if err := s.clearFileWithVec(ctx, filepath); err == nil {
			return nil
		}
		s.setVecEnabled(false)
	}

	if _, err := s.db.ExecContext(ctx, "DELETE FROM file_chunks WHERE filepath = ?", filepath); err != nil {
		return err
	}
	return nil
}

func (s *Store) clearFileWithVec(ctx context.Context, filepath string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		DELETE FROM vec_chunks WHERE rowid IN (
			SELECT rowid FROM file_chunks WHERE filepath = ?
		)
	`, filepath)
	if err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, "DELETE FROM file_chunks WHERE filepath = ?", filepath); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Search(ctx context.Context, embedding []float32, limit int) ([]Chunk, error) {
	ctx = normalizeContext(ctx)

	if err := s.EnsureReady(ctx); err != nil {
		return nil, err
	}
	if len(embedding) == 0 {
		return nil, errors.New("query embedding is required")
	}
	if limit <= 0 {
		limit = defaultSearchLimit
	}

	if s.isVecEnabled() && len(embedding) == sqliteVecDimensions {
		if chunks, err := s.searchWithVec(ctx, embedding, limit); err == nil {
			return chunks, nil
		}
		s.setVecEnabled(false)
	}

	return s.searchFallback(ctx, embedding, limit)
}

func (s *Store) searchWithVec(ctx context.Context, embedding []float32, limit int) ([]Chunk, error) {
	vecBlob, err := sqlite_vec.SerializeFloat32(embedding)
	if err != nil {
		return nil, fmt.Errorf("serialize query vector: %w", err)
	}

	query := `
	SELECT id, filepath, chunk_index, content
	FROM (
		SELECT
			f.id AS id,
			f.filepath AS filepath,
			f.chunk_index AS chunk_index,
			f.content AS content,
			distance AS distance
		FROM vec_chunks
		JOIN file_chunks f ON f.rowid = vec_chunks.rowid
		WHERE vec_chunks.embedding MATCH ?
	)
	WHERE distance <= ?
	ORDER BY distance ASC
	LIMIT ?
	`

	rows, err := s.db.QueryContext(ctx, query, vecBlob, defaultDistanceThreshold, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	chunks := make([]Chunk, 0, limit)
	for rows.Next() {
		var c Chunk
		if err := rows.Scan(&c.ID, &c.Filepath, &c.ChunkIndex, &c.Content); err != nil {
			return nil, err
		}
		chunks = append(chunks, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return chunks, nil
}

func (s *Store) searchFallback(ctx context.Context, embedding []float32, limit int) ([]Chunk, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, filepath, chunk_index, content, embedding FROM file_chunks")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type scoredChunk struct {
		chunk    Chunk
		distance float64
	}
	scored := make([]scoredChunk, 0, limit)
	for rows.Next() {
		var c Chunk
		var embeddingJSON string
		if err := rows.Scan(&c.ID, &c.Filepath, &c.ChunkIndex, &c.Content, &embeddingJSON); err != nil {
			return nil, err
		}
		var candidate []float32
		if err := json.Unmarshal([]byte(embeddingJSON), &candidate); err != nil {
			return nil, fmt.Errorf("decode embedding for chunk %q: %w", c.ID, err)
		}
		distance, err := cosineDistance(embedding, candidate)
		if err != nil {
			continue
		}
		if distance > defaultDistanceThreshold {
			continue
		}
		scored = append(scored, scoredChunk{chunk: c, distance: distance})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.Slice(scored, func(i, j int) bool {
		return scored[i].distance < scored[j].distance
	})
	if len(scored) > limit {
		scored = scored[:limit]
	}

	chunks := make([]Chunk, 0, len(scored))
	for _, item := range scored {
		chunks = append(chunks, item.chunk)
	}
	return chunks, nil
}

func cosineDistance(a, b []float32) (float64, error) {
	if len(a) == 0 || len(b) == 0 {
		return 0, errors.New("embedding cannot be empty")
	}
	if len(a) != len(b) {
		return 0, fmt.Errorf("embedding length mismatch: query=%d candidate=%d", len(a), len(b))
	}

	var dot float64
	var aNorm float64
	var bNorm float64
	for idx := range a {
		av := float64(a[idx])
		bv := float64(b[idx])
		dot += av * bv
		aNorm += av * av
		bNorm += bv * bv
	}
	if aNorm == 0 || bNorm == 0 {
		return 1, nil
	}

	similarity := dot / (math.Sqrt(aNorm) * math.Sqrt(bNorm))
	if similarity > 1 {
		similarity = 1
	}
	if similarity < -1 {
		similarity = -1
	}
	return 1 - similarity, nil
}
