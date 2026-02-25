package rag

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestChunkText verifies that chunkText successfully splits and overlaps
func TestChunkText(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		chunkSize     int
		overlap       int
		expectedCount int
	}{
		{
			name:          "small text no chunks",
			input:         "word1 word2 word3",
			chunkSize:     10,
			overlap:       2,
			expectedCount: 1,
		},
		{
			name:          "exactly one chunk",
			input:         "word1 word2 word3 word4 word5",
			chunkSize:     5,
			overlap:       1,
			expectedCount: 1,
		},
		{
			name:          "two chunks with overlap",
			input:         "w1 w2 w3 w4 w5",
			chunkSize:     4,
			overlap:       2,
			expectedCount: 2, // "w1 w2 w3 w4", "w3 w4 w5"
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunks := chunkText(tt.input, tt.chunkSize, tt.overlap)
			assert.Equal(t, tt.expectedCount, len(chunks))
		})
	}
}
