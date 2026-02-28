package tui

import (
	"sort"
	"strings"

	"github.com/yubzen/orchestra/internal/config"
	"github.com/yubzen/orchestra/internal/providers"
)

type AuthMethodKind string

const (
	AuthMethodAPIKey AuthMethodKind = "api_key"
)

type AuthMethod struct {
	Name       string
	Kind       AuthMethodKind
	Available  bool
	InputLabel string
	InputHint  string
}

type ProviderCatalog struct {
	Name      string
	KeyName   string
	Discovery providers.DiscoveryConfig
	AuthModes []AuthMethod
}

func defaultProviderCatalog(cfg *config.Config) []ProviderCatalog {
	openAIBaseURL := "https://api.openai.com/v1"
	if cfg != nil && strings.TrimSpace(cfg.Providers.OpenAI.BaseURL) != "" {
		openAIBaseURL = strings.TrimSpace(cfg.Providers.OpenAI.BaseURL)
	}

	catalog := []ProviderCatalog{
		{
			Name:    "Anthropic",
			KeyName: "anthropic",
			Discovery: providers.DiscoveryConfig{
				Kind: providers.DiscoveryKindAnthropic,
			},
			AuthModes: []AuthMethod{
				{
					Name:       "Manually enter API Key",
					Kind:       AuthMethodAPIKey,
					Available:  true,
					InputLabel: "API key",
					InputHint:  "Paste your provider API key.",
				},
			},
		},
		{
			Name:    "OpenAI",
			KeyName: "openai",
			Discovery: providers.DiscoveryConfig{
				Kind:    providers.DiscoveryKindOpenAICompat,
				KeyName: "openai",
				BaseURL: openAIBaseURL,
			},
			AuthModes: []AuthMethod{
				{
					Name:       "Manually enter API Key",
					Kind:       AuthMethodAPIKey,
					Available:  true,
					InputLabel: "API key",
					InputHint:  "Paste your provider API key.",
				},
			},
		},
		{
			Name:    "OpenRouter",
			KeyName: "openrouter",
			Discovery: providers.DiscoveryConfig{
				Kind:    providers.DiscoveryKindOpenRouter,
				KeyName: "openrouter",
				BaseURL: "https://openrouter.ai/api/v1",
			},
			AuthModes: []AuthMethod{
				{
					Name:       "Manually enter API Key",
					Kind:       AuthMethodAPIKey,
					Available:  true,
					InputLabel: "API key",
					InputHint:  "300+ models from every major provider. Free tier available.",
				},
			},
		},
		{
			Name:    "xAI",
			KeyName: "xai",
			Discovery: providers.DiscoveryConfig{
				Kind:    providers.DiscoveryKindOpenAICompat,
				KeyName: "xai",
				BaseURL: "https://api.x.ai/v1",
			},
			AuthModes: []AuthMethod{
				{
					Name:       "Manually enter API Key",
					Kind:       AuthMethodAPIKey,
					Available:  true,
					InputLabel: "API key",
					InputHint:  "Paste your provider API key.",
				},
			},
		},
	}

	return catalog
}

func storeProviderKey(keyName, key string) error {
	return providers.StoreCredential(keyName, strings.TrimSpace(key))
}

func storedProviderKey(keyName string) string {
	key, err := providers.LoadCredential(strings.TrimSpace(keyName))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(key)
}

func uniqueSortedModels(models []string) []string {
	seen := make(map[string]struct{}, len(models))
	unique := make([]string, 0, len(models))
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if _, ok := seen[model]; ok {
			continue
		}
		seen[model] = struct{}{}
		unique = append(unique, model)
	}
	sort.Strings(unique)
	return unique
}
