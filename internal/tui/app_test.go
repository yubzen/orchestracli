package tui

import (
	"strings"
	"testing"

	"github.com/yubzen/orchestra/internal/agent"
	"github.com/yubzen/orchestra/internal/state"
)

func TestRefreshConnectOptionsShowsDiscoveryConnectionState(t *testing.T) {
	t.Parallel()

	app := NewAppModel(nil, nil, nil, nil)

	for _, option := range app.connectModal.Options {
		if !strings.Contains(option.Label, "(not connected)") {
			t.Fatalf("expected provider to default to not connected, got %q", option.Label)
		}
	}

	app.discoveredModels["openrouter"] = []string{"google/gemma-3-12b-it:free [free]"}
	app.refreshConnectOptions()

	foundConnected := false
	for _, option := range app.connectModal.Options {
		if strings.HasPrefix(option.Label, "OpenRouter") && strings.Contains(option.Label, "(connected)") {
			foundConnected = true
		}
	}
	if !foundConnected {
		t.Fatal("expected OpenRouter to be marked connected after model discovery")
	}
}

func TestNormalizeSelectedModelIDOpenRouterFreeSuffix(t *testing.T) {
	t.Parallel()

	if got := normalizeSelectedModelID("openrouter", "google/gemma-3-12b-it:free [free]"); got != "google/gemma-3-12b-it:free" {
		t.Fatalf("unexpected normalized OpenRouter model: %q", got)
	}
	if got := normalizeSelectedModelID("openai", "gpt-4o [free]"); got != "gpt-4o [free]" {
		t.Fatalf("unexpected non-OpenRouter normalization: %q", got)
	}
}

func TestOpenAPIKeyModalPrefillsStoredCredential(t *testing.T) {
	originalLoader := loadProviderCredential
	defer func() {
		loadProviderCredential = originalLoader
	}()

	loadProviderCredential = func(providerKey string) string {
		if providerKey == "openrouter" {
			return "sk-or-v1-test"
		}
		return ""
	}

	app := NewAppModel(nil, nil, nil, nil)
	provider, ok := app.providerByKey("openrouter")
	if !ok {
		t.Fatal("expected openrouter provider in catalog")
	}
	if len(provider.AuthModes) == 0 {
		t.Fatal("expected at least one auth mode")
	}

	app.openAPIKeyModal(provider, provider.AuthModes[0])
	if !app.apiKeyModal.Visible {
		t.Fatal("expected api key modal to be visible")
	}
	if got := app.apiKeyModal.Value; got != "sk-or-v1-test" {
		t.Fatalf("expected prefilled key, got %q", got)
	}
}

func TestToggleExecutionMode(t *testing.T) {
	t.Parallel()

	session := &state.Session{
		ID:            "s1",
		Mode:          "orchestrated",
		ExecutionMode: state.ExecutionModeFast,
	}
	app := NewAppModel(nil, nil, session, nil)

	msg := app.toggleExecutionMode()
	if !strings.Contains(msg, "PLAN") {
		t.Fatalf("expected toggle message to mention PLAN, got %q", msg)
	}
	if got := app.session.ExecutionMode; got != state.ExecutionModePlan {
		t.Fatalf("expected plan mode after toggle, got %q", got)
	}
}

func TestHandleAgentEventThinkingCollapse(t *testing.T) {
	t.Parallel()

	app := NewAppModel(nil, nil, nil, nil)
	app.handleAgentEvent(agent.AgentEvent{
		Type:   agent.EventThinking,
		Role:   agent.RolePlanner,
		Detail: "planning task",
	})
	app.handleAgentEvent(agent.AgentEvent{
		Type:   agent.EventWriting,
		Role:   agent.RolePlanner,
		Detail: "writing .orchestra/plans/task_001.md",
	})

	if len(app.chat.messages) < 2 {
		t.Fatalf("expected thinking + collapsed/update messages, got %d", len(app.chat.messages))
	}
	last := app.chat.messages[len(app.chat.messages)-1]
	if !strings.Contains(last.Content, "writing") {
		t.Fatalf("expected event line to mention writing, got %q", last.Content)
	}
}
