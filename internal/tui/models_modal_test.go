package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestModelsModalTabFilteringAndSearch(t *testing.T) {
	t.Parallel()

	modal := NewModelsModal([]ModelOption{
		{ProviderName: "OpenRouter", ProviderKey: "openrouter", ModelID: "google/gemma-3-12b-it:free [free]"},
		{ProviderName: "OpenRouter", ProviderKey: "openrouter", ModelID: "openai/gpt-4o"},
		{ProviderName: "OpenAI", ProviderKey: "openai", ModelID: "gpt-4.1"},
	})

	if got := len(modal.filtered); got != 3 {
		t.Fatalf("expected 3 models in ALL tab, got %d", got)
	}

	_ = modal.Update(tea.KeyMsg{Type: tea.KeyTab})
	if modal.activeTab != modelFilterFree {
		t.Fatalf("expected FREE tab to be active, got %v", modal.activeTab)
	}
	if got := len(modal.filtered); got != 1 {
		t.Fatalf("expected 1 model in FREE tab, got %d", got)
	}

	for _, r := range "gemma" {
		_ = modal.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := len(modal.filtered); got != 1 {
		t.Fatalf("expected search to keep 1 match, got %d", got)
	}

	_ = modal.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if modal.query != "gemm" {
		t.Fatalf("expected query to be trimmed, got %q", modal.query)
	}
}

func TestModelsModalSelectByProviderResetsHiddenFilters(t *testing.T) {
	t.Parallel()

	modal := NewModelsModal([]ModelOption{
		{ProviderName: "OpenRouter", ProviderKey: "openrouter", ModelID: "google/gemma-3-12b-it:free [free]"},
		{ProviderName: "OpenAI", ProviderKey: "openai", ModelID: "gpt-4.1"},
	})

	_ = modal.Update(tea.KeyMsg{Type: tea.KeyTab})
	for _, r := range "gemma" {
		_ = modal.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	modal.SelectByProviderAndModel("openai", "gpt-4.1")
	selected, ok := modal.SelectedModel()
	if !ok {
		t.Fatal("expected a selected model")
	}
	if selected.ModelID != "gpt-4.1" {
		t.Fatalf("expected selected model gpt-4.1, got %q", selected.ModelID)
	}
	if modal.activeTab != modelFilterAll {
		t.Fatalf("expected selection to reset tab to ALL, got %v", modal.activeTab)
	}
	if modal.query != "" {
		t.Fatalf("expected selection to clear query, got %q", modal.query)
	}
}
