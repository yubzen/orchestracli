package tui

import (
	"strings"
	"testing"
)

func TestFilterSlashCommandsRootShowsLimitedList(t *testing.T) {
	got := filterSlashCommands("/", 6)
	if len(got) != 6 {
		t.Fatalf("expected 6 commands, got %d", len(got))
	}
}

func TestFilterSlashCommandsWithoutSlashShowsNoSuggestions(t *testing.T) {
	got := filterSlashCommands("role", 6)
	if len(got) != 0 {
		t.Fatalf("expected no suggestions without slash, got %d", len(got))
	}
}

func TestHandleSlashCommandFallbackSuggestion(t *testing.T) {
	msg, ok := handleSlashCommand("/mode", nil)().(CommandResultMsg)
	if !ok {
		t.Fatal("expected CommandResultMsg")
	}
	if !strings.Contains(msg.Msg, "Did you mean /models?") {
		t.Fatalf("expected fallback suggestion to mention /models, got %q", msg.Msg)
	}
}

func TestHandleSlashCommandModelsOpensModal(t *testing.T) {
	_, ok := handleSlashCommand("/models", nil)().(OpenModelsModalMsg)
	if !ok {
		t.Fatal("expected OpenModelsModalMsg for /models")
	}
}

func TestHandleSlashCommandRolesOpensModal(t *testing.T) {
	_, ok := handleSlashCommand("/roles", nil)().(OpenRolesModalMsg)
	if !ok {
		t.Fatal("expected OpenRolesModalMsg for /roles")
	}
}

func TestHandleSlashCommandConnectOpensModal(t *testing.T) {
	_, ok := handleSlashCommand("/connect", nil)().(OpenConnectModalMsg)
	if !ok {
		t.Fatal("expected OpenConnectModalMsg for /connect")
	}
}

func TestHandleSlashCommandKeyAliasOpensConnectModal(t *testing.T) {
	_, ok := handleSlashCommand("/key", nil)().(OpenConnectModalMsg)
	if !ok {
		t.Fatal("expected OpenConnectModalMsg for /key alias")
	}
}
