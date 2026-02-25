package tui

import (
	"strings"
	"testing"
)

func TestWrapToWidthBreaksLongWords(t *testing.T) {
	t.Parallel()

	text := strings.Repeat("x", 25)
	wrapped := wrapToWidth(text, 10)
	lines := strings.Split(wrapped, "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 wrapped lines, got %d: %q", len(lines), wrapped)
	}
	if lines[0] != strings.Repeat("x", 10) {
		t.Fatalf("unexpected first line: %q", lines[0])
	}
	if lines[1] != strings.Repeat("x", 10) {
		t.Fatalf("unexpected second line: %q", lines[1])
	}
	if lines[2] != strings.Repeat("x", 5) {
		t.Fatalf("unexpected third line: %q", lines[2])
	}
}

func TestWrapWithPrefixKeepsContinuationIndented(t *testing.T) {
	t.Parallel()

	wrapped := wrapWithPrefix("You: ", "abcdefghijk", 8)
	lines := strings.Split(wrapped, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected wrapped output with multiple lines, got %q", wrapped)
	}
	if !strings.HasPrefix(lines[0], "You: ") {
		t.Fatalf("expected first line to include prefix, got %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "     ") {
		t.Fatalf("expected continuation line to be indented, got %q", lines[1])
	}
}
