package agent

import (
	"context"
	"errors"
	"testing"
)

func TestAgentRunReturnsUserCancelledSentinel(t *testing.T) {
	t.Parallel()

	provider := &captureProvider{}
	ag := NewAgent(RoleCoder, "mock-model", provider, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := ag.RunWithOptions(ctx, "implement a parser", nil, nil, RunOptions{Mode: DispatchModeTask})
	if !errors.Is(err, ErrUserCancelled) {
		t.Fatalf("expected ErrUserCancelled, got %v", err)
	}
}

func TestOrchestratorRunReturnsUserCancelledSentinel(t *testing.T) {
	t.Parallel()

	orc := &Orchestrator{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := orc.Run(ctx, "implement feature")
	if !errors.Is(err, ErrUserCancelled) {
		t.Fatalf("expected ErrUserCancelled, got %v", err)
	}
}
