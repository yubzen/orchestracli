package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/yubzen/orchestra/internal/providers"
)

type captureProvider struct {
	systemPrompts []string
}

func (p *captureProvider) Name() string { return "capture" }

func (p *captureProvider) Ping(ctx context.Context) error { return nil }

func (p *captureProvider) ListModels(ctx context.Context) ([]string, error) {
	return []string{"mock-model"}, nil
}

func (p *captureProvider) Complete(ctx context.Context, model string, messages []providers.Message, tools []providers.Tool, onToken providers.TokenCallback) (providers.CompletionResponse, error) {
	for _, msg := range messages {
		if msg.Role == "system" {
			p.systemPrompts = append(p.systemPrompts, msg.Content)
			break
		}
	}
	reply := "ok"
	if onToken != nil {
		onToken(reply)
	}
	return providers.CompletionResponse{Text: reply}, nil
}

func TestIsTaskMessage(t *testing.T) {
	t.Parallel()

	if !isTaskMessage("please implement this feature") {
		t.Fatal("expected task keyword to classify as task")
	}
	if isTaskMessage("hi there") {
		t.Fatal("expected greeting to classify as chat")
	}
}

func TestAgentRunSelectsChatPromptForGreeting(t *testing.T) {
	t.Parallel()

	provider := &captureProvider{}
	agent := NewAgent(RoleCoder, "mock-model", provider, nil, nil)

	if _, err := agent.Run(context.Background(), "hi", nil, nil); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if len(provider.systemPrompts) == 0 {
		t.Fatal("expected at least one system prompt")
	}
	last := provider.systemPrompts[len(provider.systemPrompts)-1]
	if !strings.Contains(last, "Chat mode is active.") {
		t.Fatalf("expected chat prompt, got %q", last)
	}
}

func TestAgentRunSelectsTaskPromptForTaskRequest(t *testing.T) {
	t.Parallel()

	provider := &captureProvider{}
	agent := NewAgent(RoleCoder, "mock-model", provider, nil, nil)

	if _, err := agent.Run(context.Background(), "implement a parser", nil, nil); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if len(provider.systemPrompts) == 0 {
		t.Fatal("expected at least one system prompt")
	}
	last := provider.systemPrompts[len(provider.systemPrompts)-1]
	if !strings.Contains(last, "Task mode is active.") {
		t.Fatalf("expected task prompt, got %q", last)
	}
}
