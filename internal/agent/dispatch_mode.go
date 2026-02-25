package agent

import (
	"fmt"
	"strings"
)

type DispatchMode string

const (
	DispatchModeTask DispatchMode = "task"
	DispatchModeChat DispatchMode = "chat"
)

func (m DispatchMode) Normalize() DispatchMode {
	switch strings.ToLower(strings.TrimSpace(string(m))) {
	case string(DispatchModeTask):
		return DispatchModeTask
	default:
		return DispatchModeChat
	}
}

func isTaskMessage(input string) bool {
	input = strings.TrimSpace(strings.ToLower(input))
	if input == "" {
		return false
	}
	taskKeywords := []string{"implement", "create", "fix", "refactor", "add", "build", "write", "delete", "update", "run"}
	for _, kw := range taskKeywords {
		if strings.Contains(input, kw) {
			return true
		}
	}
	return false
}

func dispatchModeForInput(input string) DispatchMode {
	if isTaskMessage(input) {
		return DispatchModeTask
	}
	return DispatchModeChat
}

func buildTaskSystemPrompt(role Role, basePrompt string) string {
	basePrompt = strings.TrimSpace(basePrompt)
	if basePrompt == "" {
		basePrompt = fmt.Sprintf("You are the %s agent.", strings.TrimSpace(string(role)))
	}
	taskContract := `Task mode is active.
- Follow your role contract exactly.
- If your role requires structured output (JSON/YAML), keep that exact format.
- Do not switch to casual chit-chat in task mode.`
	return strings.TrimSpace(basePrompt + "\n\n" + taskContract)
}

func buildChatSystemPrompt(role Role) string {
	roleName := strings.Title(strings.ToLower(strings.TrimSpace(string(role))))
	if roleName == "" {
		roleName = "Assistant"
	}
	return strings.TrimSpace(fmt.Sprintf(`You are Orchestra's %s assistant.
Chat mode is active.
- Reply in plain, conversational text.
- Do NOT output JSON, YAML, status objects, or task result schemas unless the user explicitly asks for that format.
- If the user greets you or asks a simple question, answer directly and naturally.`, roleName))
}
