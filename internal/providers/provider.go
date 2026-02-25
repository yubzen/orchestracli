package providers

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

type Message struct {
	Role       string     // "user" | "assistant" | "system" | "tool"
	Content    string     // text content
	ToolCalls  []ToolCall // populated when role == "assistant" and LLM invoked tools
	ToolCallID string     // populated when role == "tool" (result of a tool call)
}

type Tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"input_schema"`
}

// ToolCall represents a single tool invocation requested by the LLM.
type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// CompletionResponse is the structured result from a provider's Complete call.
type CompletionResponse struct {
	Text       string     // final text content (may be empty when tool calls are present)
	ToolCalls  []ToolCall // tool invocations requested by the LLM
	StopReason string     // provider-specific stop reason ("end_turn", "tool_use", "stop", "tool_calls", etc.)
}

// HasToolCalls returns true if the LLM requested tool invocations.
func (r CompletionResponse) HasToolCalls() bool {
	return len(r.ToolCalls) > 0
}

type TokenCallback func(token string)

type Provider interface {
	Name() string
	Complete(ctx context.Context, model string, messages []Message, tools []Tool, onToken TokenCallback) (CompletionResponse, error)
	ListModels(ctx context.Context) ([]string, error)
	Ping(ctx context.Context) error
}

// ModelDiscovery is the minimal contract required to fetch models after auth.
type ModelDiscovery interface {
	ListModels(ctx context.Context) ([]string, error)
}

var ErrEmptyCredential = errors.New("credential cannot be empty")

func ValidateCredential(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return ErrEmptyCredential
	}
	return nil
}

func DiscoverModels(ctx context.Context, discovery ModelDiscovery) ([]string, error) {
	if discovery == nil {
		return nil, errors.New("model discovery is nil")
	}
	models, err := discovery.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(models))
	normalized := make([]string, 0, len(models))
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if _, ok := seen[model]; ok {
			continue
		}
		seen[model] = struct{}{}
		normalized = append(normalized, model)
	}
	sort.Strings(normalized)
	return normalized, nil
}

type ProviderAuthError struct {
	ProviderName string
	Msg          string
}

func (e *ProviderAuthError) Error() string {
	return e.Msg
}
