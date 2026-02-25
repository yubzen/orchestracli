package providers

import (
	"fmt"
	"strings"
)

type DiscoveryKind string

const (
	DiscoveryKindOpenAICompat DiscoveryKind = "openai_compat"
	DiscoveryKindAnthropic    DiscoveryKind = "anthropic"
	DiscoveryKindGoogle       DiscoveryKind = "google"
	DiscoveryKindOpenRouter   DiscoveryKind = "openrouter"
)

type DiscoveryConfig struct {
	Kind    DiscoveryKind
	KeyName string
	BaseURL string
}

func NewDiscoveryProvider(cfg DiscoveryConfig) (Provider, error) {
	switch cfg.Kind {
	case DiscoveryKindOpenAICompat:
		return NewOpenAI(cfg.BaseURL, strings.TrimSpace(cfg.KeyName)), nil
	case DiscoveryKindAnthropic:
		return NewAnthropic(), nil
	case DiscoveryKindGoogle:
		return NewGoogle(), nil
	case DiscoveryKindOpenRouter:
		return NewOpenRouter(cfg.BaseURL, strings.TrimSpace(cfg.KeyName)), nil
	default:
		return nil, fmt.Errorf("unknown discovery kind %q", cfg.Kind)
	}
}
