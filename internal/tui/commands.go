package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type CommandResultMsg struct {
	Msg string
}

type OpenModelsModalMsg struct{}
type OpenRolesModalMsg struct{}
type OpenConnectModalMsg struct{}

type slashCommand struct {
	Name        string
	Description string
}

var slashCommands = []slashCommand{
	{Name: "/roles", Description: "Switch active agent role"},
	{Name: "/models", Description: "List and switch models"},
	{Name: "/compact", Description: "Compact chat history"},
	{Name: "/mcps", Description: "Show MCP connections"},
	{Name: "/status", Description: "Toggle status overlay"},
	{Name: "/connect", Description: "Connect AI providers"},
}

func filterSlashCommands(input string, limit int) []slashCommand {
	if limit <= 0 {
		limit = len(slashCommands)
	}

	raw := strings.TrimSpace(input)
	if raw == "" {
		return nil
	}
	if !strings.HasPrefix(raw, "/") {
		return nil
	}

	token := strings.Fields(raw)[0]
	if token == "/" {
		if limit > len(slashCommands) {
			limit = len(slashCommands)
		}
		return slashCommands[:limit]
	}
	token = strings.TrimPrefix(token, "/")

	query := strings.ToLower(strings.TrimSpace(token))
	if query == "" {
		if limit > len(slashCommands) {
			limit = len(slashCommands)
		}
		return slashCommands[:limit]
	}

	matches := make([]slashCommand, 0, limit)
	add := func(c slashCommand) bool {
		if len(matches) >= limit {
			return false
		}
		matches = append(matches, c)
		return true
	}

	// Prefix matches first for intuitive command completion.
	for _, c := range slashCommands {
		if strings.HasPrefix(strings.TrimPrefix(strings.ToLower(c.Name), "/"), query) {
			if !add(c) {
				return matches
			}
		}
	}

	// If needed, add substring matches to help discovery.
	for _, c := range slashCommands {
		name := strings.TrimPrefix(strings.ToLower(c.Name), "/")
		if strings.HasPrefix(name, query) {
			continue
		}
		if strings.Contains(name, query) {
			if !add(c) {
				return matches
			}
		}
	}

	return matches
}

func normalizeSlashCommand(input string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return ""
	}
	parts := strings.Fields(trimmed)
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

func handleSlashCommand(cmdStr string, app *AppModel) tea.Cmd {
	return func() tea.Msg {
		switch normalizeSlashCommand(cmdStr) {
		case "/roles":
			return OpenRolesModalMsg{}
		case "/models":
			return OpenModelsModalMsg{}
		case "/connect", "/key":
			return OpenConnectModalMsg{}
		case "/compact":
			return CommandResultMsg{Msg: "History compacted to long-term memory."}
		case "/mcps":
			return CommandResultMsg{Msg: "MCP connections: None active."}
		case "/status":
			return CommandResultMsg{Msg: "Status overlay toggled."}
		default:
			if suggestions := filterSlashCommands(cmdStr, 1); len(suggestions) == 1 {
				return CommandResultMsg{Msg: fmt.Sprintf("Unknown command: %s. Did you mean %s?", cmdStr, suggestions[0].Name)}
			}
			return CommandResultMsg{Msg: fmt.Sprintf("Unknown command: %s", cmdStr)}
		}
	}
}
