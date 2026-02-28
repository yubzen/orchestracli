package tui

import (
	"strings"
	"testing"
)

func TestStatusBarShowsRepoPathOnLeft(t *testing.T) {
	t.Parallel()

	sb := NewStatusBarModel()
	sb.SetWidth(200)
	sb.SetRepoPath("/home/ayoub/work/private/orchestra")

	view := sb.View()
	if !strings.Contains(view, "/home/ayoub/work/private/orchestra") {
		t.Fatalf("expected full repo path in status bar, got %q", view)
	}
}

func TestStatusBarTruncatesRepoPathFromLeft(t *testing.T) {
	t.Parallel()

	sb := NewStatusBarModel()
	sb.SetWidth(90)
	sb.SetRepoPath("/very/long/prefix/path/that/keeps/going/private/orchestra")

	view := sb.View()
	if !strings.Contains(view, "private/orchestra") {
		t.Fatalf("expected path tail preserved, got %q", view)
	}
	if !strings.Contains(view, "…") {
		t.Fatalf("expected left-side truncation ellipsis, got %q", view)
	}
}

func TestStatusBarUsesUnsetAndNoUnassignedLabels(t *testing.T) {
	t.Parallel()

	sb := NewStatusBarModel()
	sb.SetWidth(220)

	view := strings.ToLower(sb.View())
	if strings.Contains(view, "unassigned") {
		t.Fatalf("status bar should not show unassigned label, got %q", view)
	}
	if strings.Contains(view, "unconfigured") {
		t.Fatalf("status bar should not show unconfigured label, got %q", view)
	}
	if !strings.Contains(view, "(unset)") {
		t.Fatalf("status bar should show unset state, got %q", view)
	}
}
