package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
)

func LoadSystemPrompt(role string) string {
	b, err := os.ReadFile(filepath.Join("roles", role, "persona.md"))
	if err != nil {
		return ""
	}
	return string(b)
}

func LoadAllowedTools(role string) []string {
	b, err := os.ReadFile(filepath.Join("roles", role, "tools.json"))
	if err != nil {
		return nil
	}
	var data struct {
		Allowed []string `json:"allowed"`
	}
	_ = json.Unmarshal(b, &data)
	return data.Allowed
}
