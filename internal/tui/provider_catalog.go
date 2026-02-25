package tui

import (
	"sort"
	"strings"

	"github.com/zalando/go-keyring"
)

const keyringService = "orchestra"

type AuthMethod struct {
	Name      string
	Available bool
}

type ProviderCatalog struct {
	Name      string
	KeyName   string
	Models    []string
	AuthModes []AuthMethod
}

func defaultProviderCatalog() []ProviderCatalog {
	return []ProviderCatalog{
		{
			Name:    "Anthropic",
			KeyName: "anthropic",
			Models: []string{
				"claude-3-opus-20240229",
				"claude-3-7-sonnet-latest",
				"claude-3-5-sonnet-20241022",
			},
			AuthModes: []AuthMethod{
				{Name: "Manually enter API Key", Available: true},
			},
		},
		{
			Name:    "OpenAI",
			KeyName: "openai",
			Models: []string{
				"gpt-4o",
				"gpt-4.1",
				"gpt-4.1-mini",
			},
			AuthModes: []AuthMethod{
				{Name: "ChatGPT Pro/Plus (browser)", Available: false},
				{Name: "ChatGPT Pro/Plus (headless)", Available: false},
				{Name: "Manually enter API Key", Available: true},
			},
		},
		{
			Name:    "Google",
			KeyName: "google",
			Models: []string{
				"gemini-2.5-pro",
				"gemini-2.5-flash",
				"gemini-1.5-pro",
			},
			AuthModes: []AuthMethod{
				{Name: "Manually enter API Key", Available: true},
			},
		},
		{
			Name:    "xAI",
			KeyName: "xai",
			Models: []string{
				"grok-2-1212",
				"grok-beta",
			},
			AuthModes: []AuthMethod{
				{Name: "Manually enter API Key", Available: true},
			},
		},
	}
}

func hasProviderKey(keyName string) bool {
	key, err := keyring.Get(keyringService, keyName)
	return err == nil && strings.TrimSpace(key) != ""
}

func storeProviderKey(keyName, key string) error {
	return keyring.Set(keyringService, keyName, strings.TrimSpace(key))
}

func connectedModels(catalog []ProviderCatalog) []string {
	var models []string
	for _, provider := range catalog {
		if !hasProviderKey(provider.KeyName) {
			continue
		}
		models = append(models, provider.Models...)
	}

	seen := make(map[string]bool, len(models))
	var unique []string
	for _, model := range models {
		if seen[model] {
			continue
		}
		seen[model] = true
		unique = append(unique, model)
	}
	sort.Strings(unique)
	return unique
}
