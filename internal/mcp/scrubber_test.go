package mcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestClean verifies the secret scrubber handles common secrets
func TestClean(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "env var format",
			input:    "AWS_KEY=AKIAIOSFODNN7EXAMPLE",
			expected: "AWS_KEY=[REDACTED]",
		},
		{
			name:     "openai key format",
			input:    "sk-proj-somethingreallylong1234567890abcdef",
			expected: "[REDACTED_KEY]",
		},
		{
			name:     "jwt format",
			input:    "eyJhbGciOiJIUzI1NiIsInR5cCI.eyJzdWIiOiIxMjM.SflKxwRJSMe",
			expected: "[REDACTED_JWT]",
		},
		{
			name:     "google key format",
			input:    "AIzaSyB-abcdefghijklmnopqrstuvwxyz12345",
			expected: "[REDACTED_KEY]",
		},
		{
			name:     "github format",
			input:    "ghp_abcdefghijklmnopqrstuvwxyz1234567890",
			expected: "[REDACTED_KEY]",
		},
		{
			name:     "clean string",
			input:    "Hello world this is clean",
			expected: "Hello world this is clean",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, Clean(tt.input))
		})
	}
}
