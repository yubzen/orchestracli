package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yubzen/orchestra/internal/providers"
	"github.com/yubzen/orchestra/internal/state"
)

type sequenceProvider struct {
	replies []string
	callIdx int
	prompts []string
}

func (m *sequenceProvider) Name() string                                     { return "mock" }
func (m *sequenceProvider) Ping(ctx context.Context) error                   { return nil }
func (m *sequenceProvider) ListModels(ctx context.Context) ([]string, error) { return nil, nil }
func (m *sequenceProvider) Complete(ctx context.Context, model string, messages []providers.Message, tools []providers.Tool, onToken providers.TokenCallback) (providers.CompletionResponse, error) {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			m.prompts = append(m.prompts, messages[i].Content)
			break
		}
	}
	if len(m.replies) == 0 {
		return providers.CompletionResponse{}, nil
	}
	if m.callIdx >= len(m.replies) {
		reply := m.replies[len(m.replies)-1]
		if onToken != nil {
			onToken(reply)
		}
		return providers.CompletionResponse{Text: reply}, nil
	}
	reply := m.replies[m.callIdx]
	m.callIdx++
	if onToken != nil {
		onToken(reply)
	}
	return providers.CompletionResponse{Text: reply}, nil
}

func newTestAgent(role Role, provider providers.Provider) *Agent {
	return &Agent{
		Role:             role,
		Provider:         provider,
		Model:            "test-model",
		SystemPrompt:     "test prompt",
		TaskSystemPrompt: "test task prompt",
		ChatSystemPrompt: "test chat prompt",
	}
}

func TestDeriveStrategy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		avail     roleAvailability
		want      ExecutionStrategy
		expectErr bool
	}{
		{name: "full", avail: roleAvailability{planner: true, coder: true, reviewer: true}, want: StrategyFull},
		{name: "no coder", avail: roleAvailability{planner: true, reviewer: true}, want: StrategyNoCoder},
		{name: "no planner", avail: roleAvailability{coder: true, reviewer: true}, want: StrategyNoPlanner},
		{name: "no reviewer", avail: roleAvailability{planner: true, coder: true}, want: StrategyNoReviewer},
		{name: "solo", avail: roleAvailability{coder: true}, want: StrategySolo},
		{name: "none", avail: roleAvailability{}, expectErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := deriveStrategy(tt.avail)
			if tt.expectErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}

func TestOrchestratorFlow(t *testing.T) {
	t.Parallel()
	workDir := t.TempDir()

	plannerProv := &sequenceProvider{
		replies: []string{"```yaml\ntasks:\n  - id: t1\n    description: a\n  - id: t2\n    description: b\n    depends_on: [t1]\n```"},
	}
	coderProv := &sequenceProvider{
		replies: []string{`{"status":"done"}`, `{"status":"done"}`},
	}
	reviewerProv := &sequenceProvider{
		replies: []string{`{"approved": true, "findings": []}`, `{"approved": true, "findings": []}`},
	}

	planner := newTestAgent(RolePlanner, plannerProv)
	coder := newTestAgent(RoleCoder, coderProv)
	reviewer := newTestAgent(RoleReviewer, reviewerProv)

	updateChan := make(chan StepUpdate, 100)
	orc := &Orchestrator{
		Planner:      planner,
		Coder:        coder,
		Reviewer:     reviewer,
		UpdateChan:   updateChan,
		WorkingDir:   workDir,
		ProjectBrief: "Working directory: .\nFile tree preview:\n- internal/agent/orchestrator.go",
		Session:      &state.Session{ExecutionMode: state.ExecutionModeFast},
	}

	if err := orc.Run(context.Background(), "implement something"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(plannerProv.prompts) == 0 || !strings.Contains(plannerProv.prompts[0], "Project context:") {
		t.Fatalf("expected planner prompt to include project context, got %q", plannerProv.prompts)
	}
	plansDir := filepath.Join(workDir, ".orchestra", "plans")
	entries, err := os.ReadDir(plansDir)
	if err != nil {
		t.Fatalf("expected plans directory to exist: %v", err)
	}
	hasPlan := false
	hasLock := false
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, ".md") {
			hasPlan = true
		}
		if strings.HasSuffix(name, ".lock") {
			hasLock = true
		}
	}
	if !hasPlan || !hasLock {
		t.Fatalf("expected both plan and lock files, got entries: %#v", entries)
	}

	close(updateChan)
	var events []string
	for e := range updateChan {
		events = append(events, e.StepID+"-"+e.Status)
	}
	eventsStr := strings.Join(events, " ")
	if !strings.Contains(eventsStr, "planner-done") {
		t.Fatalf("expected planner done event, got %q", eventsStr)
	}
	if !strings.Contains(eventsStr, "t1-done") {
		t.Fatalf("expected t1 done event, got %q", eventsStr)
	}
	if !strings.Contains(eventsStr, "t2-done") {
		t.Fatalf("expected t2 done event, got %q", eventsStr)
	}
}

func TestOrchestratorPlanModeRequiresApproval(t *testing.T) {
	t.Parallel()
	workDir := t.TempDir()

	plannerProv := &sequenceProvider{
		replies: []string{"```yaml\ntasks:\n  - id: t1\n    description: a\n```"},
	}
	coderProv := &sequenceProvider{
		replies: []string{`{"status":"done"}`},
	}
	reviewerProv := &sequenceProvider{
		replies: []string{`{"approved": true, "findings": []}`},
	}

	orc := &Orchestrator{
		Planner:          newTestAgent(RolePlanner, plannerProv),
		Coder:            newTestAgent(RoleCoder, coderProv),
		Reviewer:         newTestAgent(RoleReviewer, reviewerProv),
		UpdateChan:       make(chan StepUpdate, 100),
		PlanApprovalChan: make(chan PlanApproval, 1),
		WorkingDir:       workDir,
		ProjectBrief:     "Working directory: .",
		Session:          &state.Session{ExecutionMode: state.ExecutionModePlan},
	}

	done := make(chan error, 1)
	go func() {
		done <- orc.Run(context.Background(), "build feature")
	}()

	var planID string
	timeout := time.After(2 * time.Second)
	for {
		select {
		case <-timeout:
			t.Fatal("timed out waiting for plan_ready event")
		case up := <-orc.UpdateChan:
			if up.Status == "plan_ready" {
				planID = up.PlanID
				goto approve
			}
		}
	}

approve:
	orc.SubmitPlanApproval(PlanApproval{
		PlanID:   planID,
		Approved: true,
	})

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("orchestrator did not finish after approval")
	}
}

func TestOrchestratorNoPlannerFallsBackToCoder(t *testing.T) {
	t.Parallel()
	workDir := t.TempDir()

	coderProv := &sequenceProvider{
		replies: []string{`{"status":"done"}`},
	}
	reviewerProv := &sequenceProvider{
		replies: []string{`{"approved": true, "findings": []}`},
	}

	orc := &Orchestrator{
		Coder:        newTestAgent(RoleCoder, coderProv),
		Reviewer:     newTestAgent(RoleReviewer, reviewerProv),
		UpdateChan:   make(chan StepUpdate, 50),
		WorkingDir:   workDir,
		ProjectBrief: "Working directory: .",
		Session:      &state.Session{ExecutionMode: state.ExecutionModeFast},
	}

	if err := orc.Run(context.Background(), "implement feature"); err != nil {
		t.Fatalf("run: %v", err)
	}
}

func TestOrchestratorConversationalBypassesPlanning(t *testing.T) {
	t.Parallel()
	workDir := t.TempDir()

	coderProv := &sequenceProvider{
		replies: []string{"hello back"},
	}
	events := make(chan AgentEvent, 16)
	orc := &Orchestrator{
		Coder:      newTestAgent(RoleCoder, coderProv),
		UpdateChan: make(chan StepUpdate, 16),
		EventChan:  events,
		WorkingDir: workDir,
		Session:    &state.Session{ExecutionMode: state.ExecutionModeFast},
	}

	if err := orc.Run(context.Background(), "hi"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(coderProv.prompts) == 0 || !strings.Contains(strings.ToLower(coderProv.prompts[0]), "hi") {
		t.Fatalf("expected conversational prompt to reach responder, got %#v", coderProv.prompts)
	}
	if _, err := os.Stat(filepath.Join(workDir, ".orchestra", "plans")); !os.IsNotExist(err) {
		t.Fatalf("expected conversational run to skip planning files, stat err=%v", err)
	}

	foundDone := false
	close(events)
	for event := range events {
		if event.Type == EventDone {
			foundDone = true
			break
		}
	}
	if !foundDone {
		t.Fatal("expected done event for conversational response")
	}
}
