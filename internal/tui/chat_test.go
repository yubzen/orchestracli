package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestChatModelSelectedSuggestionDefaultsToFirst(t *testing.T) {
	m := NewChatModel()
	m.textInput.SetValue("/m")
	m.updateSlashSuggestions()

	selected, ok := m.SelectedSlashSuggestion()
	if !ok {
		t.Fatal("expected a selected suggestion")
	}
	if selected.Name != "/models" {
		t.Fatalf("expected first suggestion /models, got %q", selected.Name)
	}
}

func TestChatModelNoSuggestionsWithoutSlash(t *testing.T) {
	m := NewChatModel()
	m.textInput.SetValue("m")
	m.updateSlashSuggestions()

	if len(m.slashSuggestions) != 0 {
		t.Fatalf("expected no suggestions without slash, got %d", len(m.slashSuggestions))
	}
}

func TestChatModelMoveSlashSelection(t *testing.T) {
	m := NewChatModel()
	m.textInput.SetValue("/")
	m.updateSlashSuggestions()

	if !m.MoveSlashSelection(1) {
		t.Fatal("expected movement to be handled")
	}
	selected, ok := m.SelectedSlashSuggestion()
	if !ok {
		t.Fatal("expected a selected suggestion")
	}
	if selected.Name != "/models" {
		t.Fatalf("expected second suggestion /models, got %q", selected.Name)
	}

	m.MoveSlashSelection(100)
	selected, ok = m.SelectedSlashSuggestion()
	if !ok {
		t.Fatal("expected a selected suggestion after moving down")
	}
	if selected.Name != "/connect" {
		t.Fatalf("expected last suggestion /connect, got %q", selected.Name)
	}

	m.MoveSlashSelection(-100)
	selected, ok = m.SelectedSlashSuggestion()
	if !ok {
		t.Fatal("expected a selected suggestion after moving up")
	}
	if selected.Name != "/roles" {
		t.Fatalf("expected first suggestion /roles, got %q", selected.Name)
	}
}

func TestChatModelSelectionPersistsWithoutInputChange(t *testing.T) {
	m := NewChatModel()
	m.textInput.SetValue("/")
	m.updateSlashSuggestions()

	if !m.MoveSlashSelection(1) {
		t.Fatal("expected movement to be handled")
	}
	selected, ok := m.SelectedSlashSuggestion()
	if !ok {
		t.Fatal("expected selected suggestion")
	}
	if selected.Name != "/models" {
		t.Fatalf("expected /models after moving down, got %q", selected.Name)
	}

	// Simulate non-input event refresh; selection should not jump back to first.
	m.updateSlashSuggestions()
	selected, ok = m.SelectedSlashSuggestion()
	if !ok {
		t.Fatal("expected selected suggestion after refresh")
	}
	if selected.Name != "/models" {
		t.Fatalf("expected selection to persist on refresh, got %q", selected.Name)
	}

	// Typing changes input, so selection should default back to first match.
	m.textInput.SetValue("/m")
	m.updateSlashSuggestions()
	selected, ok = m.SelectedSlashSuggestion()
	if !ok {
		t.Fatal("expected selected suggestion after input change")
	}
	if selected.Name != "/models" {
		t.Fatalf("expected first match /models after typing, got %q", selected.Name)
	}
}

func TestApplyTopSlashSuggestionMovesCursorToEnd(t *testing.T) {
	m := NewChatModel()
	m.textInput.SetValue("/mod")
	m.textInput.SetCursor(1)
	m.updateSlashSuggestions()

	if !m.ApplyTopSlashSuggestion() {
		t.Fatal("expected tab autocomplete to apply suggestion")
	}
	if got := m.textInput.Value(); got != "/models" {
		t.Fatalf("expected /models after autocomplete, got %q", got)
	}
	if got, want := m.textInput.Position(), len([]rune("/models")); got != want {
		t.Fatalf("expected cursor at end (%d), got %d", want, got)
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m = updated.(*ChatModel)
	if got := m.textInput.Value(); got != "/modelsx" {
		t.Fatalf("expected typing after tab to append at end, got %q", got)
	}
}

func TestEmptyStateViewContainsOrchestraAndTip(t *testing.T) {
	m := NewChatModel()
	m.SetSize(120, 30)

	view := m.View()
	if !strings.Contains(strings.ToLower(view), "o r c h e s t r a") {
		t.Fatalf("expected empty state logo to contain orchestra, got %q", view)
	}
	if !strings.Contains(strings.ToLower(view), "run /connect") {
		t.Fatalf("expected empty state tip to mention /connect, got %q", view)
	}
	if strings.Contains(strings.ToLower(view), "ctrl+t variants") {
		t.Fatalf("expected empty state to not show shortcuts hint, got %q", view)
	}
}
