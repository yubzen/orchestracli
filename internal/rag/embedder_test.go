package rag

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEmbedderGuardOnNilReceiver(t *testing.T) {
	var embedder *Embedder

	assert.ErrorIs(t, embedder.EnsureReady(context.Background()), ErrEmbedderNotReady)

	_, err := embedder.Embed(context.Background(), "hello")
	assert.ErrorIs(t, err, ErrEmbedderNotReady)
}

func TestEmbedderRequiresConfig(t *testing.T) {
	embedder := &Embedder{}

	assert.ErrorContains(t, embedder.EnsureReady(context.Background()), "URL is empty")

	_, err := embedder.Embed(context.Background(), "hello")
	assert.ErrorContains(t, err, "URL is empty")
}

func TestNewEmbedderDefaults(t *testing.T) {
	embedder := NewEmbedder("http://localhost:11434/", "")

	assert.Equal(t, "http://localhost:11434", embedder.URL)
	assert.Equal(t, "nomic-embed-text", embedder.Model)
}
