package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/yubzen/orchestra/internal/providers"
	"github.com/stretchr/testify/assert"
)

// MockProvider returns predefined replies
type MockProvider struct {
	Reply string
}

func (m MockProvider) Name() string                                     { return "mock" }
func (m MockProvider) Ping(ctx context.Context) error                   { return nil }
func (m MockProvider) ListModels(ctx context.Context) ([]string, error) { return nil, nil }
func (m MockProvider) Complete(ctx context.Context, model string, messages []providers.Message, tools []providers.Tool) (string, error) {
	return m.Reply, nil
}

// TestOrchestratorFlow tests the happy path for the full orchestrator pipeline
func TestOrchestratorFlow(t *testing.T) {
	plannerProv := &MockProvider{
		Reply: "```yaml\ntasks:\n  - id: t1\n    description: a\n  - id: t2\n    description: b\n    depends_on: [t1]\n```",
	}
	coderProv := &MockProvider{
		Reply: `{"status": "done"}`,
	}
	reviewerProv := &MockProvider{
		Reply: `{"approved": true, "findings": []}`,
	}

	planner := &Agent{Role: RolePlanner, Provider: plannerProv, Model: "test-model", SystemPrompt: "planner prompt"}
	coder := &Agent{Role: RoleCoder, Provider: coderProv, Model: "test-model", SystemPrompt: "coder prompt"}
	reviewer := &Agent{Role: RoleReviewer, Provider: reviewerProv, Model: "test-model", SystemPrompt: "reviewer prompt"}

	updateChan := make(chan StepUpdate, 100)

	orc := &Orchestrator{
		Planner:    planner,
		Coder:      coder,
		Reviewer:   reviewer,
		UpdateChan: updateChan,
	}

	err := orc.Run(context.Background(), "do something")
	assert.NoError(t, err)

	close(updateChan)
	var events []string
	for e := range updateChan {
		events = append(events, e.StepID+"-"+e.Status)
	}

	// We expect: planner-running, planner-done
	// t1-running, t1-running (review), t1-done
	// t2-running, t2-running (review), t2-done
	eventsStr := strings.Join(events, " ")
	assert.Contains(t, eventsStr, "planner-done")
	assert.Contains(t, eventsStr, "t1-done")
	assert.Contains(t, eventsStr, "t2-done")
}
