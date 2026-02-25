package rag

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoreGuardOnNilReceiver(t *testing.T) {
	var store *Store

	assert.NoError(t, store.Close())
	assert.ErrorIs(t, store.EnsureReady(context.Background()), ErrStoreNotReady)
	assert.ErrorIs(t, store.SaveChunk(context.Background(), Chunk{ID: "c1"}, []float32{1}), ErrStoreNotReady)
	assert.ErrorIs(t, store.ClearFile(context.Background(), "x.go"), ErrStoreNotReady)

	_, err := store.Search(context.Background(), []float32{1}, 1)
	assert.ErrorIs(t, err, ErrStoreNotReady)
}

func TestStoreGuardOnZeroValue(t *testing.T) {
	store := &Store{}

	assert.NoError(t, store.Close())
	assert.ErrorIs(t, store.EnsureReady(context.Background()), ErrStoreNotReady)
	assert.ErrorIs(t, store.SaveChunk(context.Background(), Chunk{ID: "c1"}, []float32{1}), ErrStoreNotReady)
	assert.ErrorIs(t, store.ClearFile(context.Background(), "x.go"), ErrStoreNotReady)

	_, err := store.Search(context.Background(), []float32{1}, 1)
	assert.ErrorIs(t, err, ErrStoreNotReady)
}

func TestStoreSaveSearchAndClearWithoutExtensions(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "rag.db")

	store, err := NewStore(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, store.Close())
	})

	ctx := context.Background()
	require.NoError(t, store.SaveChunk(ctx, Chunk{
		ID:         "a:0",
		Filepath:   "a.go",
		ChunkIndex: 0,
		Content:    "chunk-a",
	}, []float32{1, 0, 0}))
	require.NoError(t, store.SaveChunk(ctx, Chunk{
		ID:         "b:0",
		Filepath:   "b.go",
		ChunkIndex: 0,
		Content:    "chunk-b",
	}, []float32{0, 1, 0}))

	matches, err := store.Search(ctx, []float32{0.9, 0.1, 0}, 5)
	require.NoError(t, err)
	require.Len(t, matches, 1)
	assert.Equal(t, "a:0", matches[0].ID)

	require.NoError(t, store.ClearFile(ctx, "a.go"))
	matches, err = store.Search(ctx, []float32{1, 0, 0}, 5)
	require.NoError(t, err)
	assert.Empty(t, matches)
}

func TestCosineDistanceValidation(t *testing.T) {
	_, err := cosineDistance(nil, []float32{1})
	assert.Error(t, err)

	_, err = cosineDistance([]float32{1, 0}, []float32{1})
	assert.ErrorContains(t, err, "length mismatch")

	dist, err := cosineDistance([]float32{1, 0}, []float32{1, 0})
	require.NoError(t, err)
	assert.InDelta(t, 0.0, dist, 1e-9)
}

func TestSQLiteVecIsRegistered(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "rag.db")
	store, err := NewStore(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, store.Close())
	})

	var vecVersion string
	err = store.db.QueryRow("SELECT vec_version()").Scan(&vecVersion)
	require.NoError(t, err)
	assert.NotEmpty(t, vecVersion)
}
