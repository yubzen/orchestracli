package providers

import "context"

type Message struct {
	Role    string // "user" | "assistant" | "system"
	Content string
}

type Tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"input_schema"`
}

type Provider interface {
	Name() string
	Complete(ctx context.Context, model string, messages []Message, tools []Tool) (string, error)
	ListModels(ctx context.Context) ([]string, error)
	Ping(ctx context.Context) error
}

type ProviderAuthError struct {
	ProviderName string
	Msg          string
}

func (e *ProviderAuthError) Error() string {
	return e.Msg
}
