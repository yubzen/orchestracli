package agent

import (
	"context"
	"encoding/json"
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

type toolLoopProvider struct {
	callIdx       int
	prompts       []string
	sawToolResult bool
}

func (p *toolLoopProvider) Name() string                                     { return "tool-loop" }
func (p *toolLoopProvider) Ping(ctx context.Context) error                   { return nil }
func (p *toolLoopProvider) ListModels(ctx context.Context) ([]string, error) { return nil, nil }
func (p *toolLoopProvider) Complete(ctx context.Context, model string, messages []providers.Message, tools []providers.Tool, onToken providers.TokenCallback) (providers.CompletionResponse, error) {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			p.prompts = append(p.prompts, messages[i].Content)
			break
		}
	}

	p.callIdx++
	switch p.callIdx {
	case 1:
		argsObj := `{"path":"hamid.ts","content":"export function hamid(): string {\n\treturn \"hamid\"\n}\n"}`
		encoded, _ := json.Marshal(argsObj)
		return providers.CompletionResponse{
			Text: "creating file",
			ToolCalls: []providers.ToolCall{
				{
					ID:        "tc-1",
					Name:      "write_file",
					Arguments: json.RawMessage(encoded),
				},
			},
			StopReason: "tool_calls",
		}, nil
	case 2:
		for _, msg := range messages {
			if msg.Role == "tool" && msg.ToolCallID == "tc-1" && strings.Contains(msg.Content, `"ok":true`) {
				p.sawToolResult = true
				break
			}
		}
		return providers.CompletionResponse{Text: `{"status":"done"}`}, nil
	default:
		return providers.CompletionResponse{Text: `{"status":"done"}`}, nil
	}
}

type planFileReadProvider struct {
	prompts           []string
	sawPlanReadResult bool
}

func (p *planFileReadProvider) Name() string                                     { return "plan-read" }
func (p *planFileReadProvider) Ping(ctx context.Context) error                   { return nil }
func (p *planFileReadProvider) ListModels(ctx context.Context) ([]string, error) { return nil, nil }
func (p *planFileReadProvider) Complete(ctx context.Context, model string, messages []providers.Message, tools []providers.Tool, onToken providers.TokenCallback) (providers.CompletionResponse, error) {
	var lastUser string
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			lastUser = messages[i].Content
			p.prompts = append(p.prompts, lastUser)
			break
		}
	}

	for _, msg := range messages {
		if msg.Role != "tool" {
			continue
		}
		if strings.Contains(msg.Content, `"path":".orchestra/plans/`) || strings.Contains(msg.Content, `"path": ".orchestra/plans/`) {
			p.sawPlanReadResult = true
			reply := `{"status":"done"}`
			if onToken != nil {
				onToken(reply)
			}
			return providers.CompletionResponse{Text: reply}, nil
		}
	}

	planPath := extractPlanPathFromPrompt(lastUser)
	if planPath == "" {
		reply := `{"status":"done"}`
		if onToken != nil {
			onToken(reply)
		}
		return providers.CompletionResponse{Text: reply}, nil
	}

	args, _ := json.Marshal(map[string]any{"path": planPath})
	return providers.CompletionResponse{
		Text: "reading approved plan file",
		ToolCalls: []providers.ToolCall{
			{
				ID:        "read-plan-1",
				Name:      "read_file",
				Arguments: args,
			},
		},
		StopReason: "tool_calls",
	}, nil
}

func extractPlanPathFromPrompt(prompt string) string {
	for _, line := range strings.Split(prompt, "\n") {
		line = strings.TrimSpace(line)
		const prefix = "Authoritative plan file:"
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

func requireFileEventually(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat %s: %v", path, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for file %s", path)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func requireDirEntryWithSuffixEventually(t *testing.T, dir, suffix string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if time.Now().After(deadline) {
				t.Fatalf("read dir %s: %v", dir, err)
			}
			time.Sleep(20 * time.Millisecond)
			continue
		}
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), suffix) {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s in %s", suffix, dir)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func eventContainsInternalOrchestraPath(event AgentEvent) bool {
	if strings.Contains(strings.ToLower(event.Detail), ".orchestra/") {
		return true
	}
	raw, err := json.Marshal(event.Payload)
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(raw)), ".orchestra/")
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
		{name: "no reviewer", avail: roleAvailability{planner: true, coder: true}, want: StrategyNoReviewer},
		{name: "planner solo", avail: roleAvailability{planner: true}, want: StrategySolo},
		{name: "missing planner with coder+reviewer", avail: roleAvailability{coder: true, reviewer: true}, expectErr: true},
		{name: "missing planner with coder only", avail: roleAvailability{coder: true}, expectErr: true},
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
	coderProv := &planFileReadProvider{}
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
		Session:      &state.Session{ExecutionMode: state.ExecutionModePlan},
	}

	if err := orc.Run(context.Background(), "implement something"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(plannerProv.prompts) == 0 || !strings.Contains(plannerProv.prompts[0], "Project context:") {
		t.Fatalf("expected planner prompt to include project context, got %q", plannerProv.prompts)
	}
	plansDir := filepath.Join(workDir, ".orchestra", "plans")
	requireDirEntryWithSuffixEventually(t, plansDir, ".md", 2*time.Second)
	requireDirEntryWithSuffixEventually(t, plansDir, ".lock", 3*time.Second)

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
	if !coderProv.sawPlanReadResult {
		t.Fatal("expected coder to read plan file via read_file tool in PLAN mode")
	}
	if len(coderProv.prompts) == 0 {
		t.Fatal("expected coder prompts to be captured")
	}
	firstPrompt := coderProv.prompts[0]
	if !strings.Contains(firstPrompt, "Authoritative plan file: .orchestra/plans/") {
		t.Fatalf("expected coder prompt to reference authoritative plan path, got %q", firstPrompt)
	}
	if strings.Contains(firstPrompt, "Task description:") {
		t.Fatalf("expected plan-mode coder prompt to avoid injected task summaries, got %q", firstPrompt)
	}
}

func TestOrchestratorPlanModeRequiresApproval(t *testing.T) {
	t.Parallel()
	workDir := t.TempDir()

	plannerProv := &sequenceProvider{
		replies: []string{"```yaml\ntasks:\n  - id: t1\n    description: a\n```"},
	}
	coderProv := &planFileReadProvider{}
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
	if len(coderProv.prompts) != 0 {
		t.Fatalf("expected no coder dispatch before approval, got %d prompt(s)", len(coderProv.prompts))
	}
	planPath := filepath.Join(workDir, ".orchestra", "plans", planID+".md")
	if _, err := os.Stat(planPath); err != nil {
		t.Fatalf("expected plan markdown before approval at %s: %v", planPath, err)
	}

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
	if len(coderProv.prompts) == 0 {
		t.Fatal("expected coder dispatch after approval")
	}
	if !coderProv.sawPlanReadResult {
		t.Fatal("expected coder to read approved plan file via read_file tool")
	}
	lockPath := filepath.Join(workDir, ".orchestra", "plans", planID+".lock")
	requireFileEventually(t, lockPath, 3*time.Second)
}

func TestOrchestratorNoCoderUsesPlannerAsExecutor(t *testing.T) {
	t.Parallel()
	workDir := t.TempDir()

	plannerProv := &sequenceProvider{
		replies: []string{
			`{"status":"done"}`,
		},
	}
	reviewerProv := &sequenceProvider{
		replies: []string{`{"approved": true, "findings": []}`},
	}
	events := make(chan AgentEvent, 128)

	orc := &Orchestrator{
		Planner:      newTestAgent(RolePlanner, plannerProv),
		Reviewer:     newTestAgent(RoleReviewer, reviewerProv),
		UpdateChan:   make(chan StepUpdate, 64),
		EventChan:    events,
		WorkingDir:   workDir,
		ProjectBrief: "Working directory: .",
		Session:      &state.Session{ExecutionMode: state.ExecutionModeFast},
	}

	if err := orc.Run(context.Background(), "implement feature"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := len(plannerProv.prompts); got != 1 {
		t.Fatalf("expected planner to receive only direct execution prompt in FAST mode, got %d", got)
	}
	if strings.Contains(strings.ToLower(plannerProv.prompts[0]), "return only valid yaml") {
		t.Fatalf("expected no planner planning prompt in FAST mode, got %q", plannerProv.prompts[0])
	}
	if _, ok := orc.Planner.ToolSet.Get("write_file"); !ok {
		t.Fatal("expected planner toolset to be elevated with write_file in no-coder strategy")
	}
	if _, ok := orc.Planner.ToolSet.Get("run_command"); !ok {
		t.Fatal("expected planner toolset to be elevated with run_command in no-coder strategy")
	}

	close(events)
	foundPlannerRunning := false
	for event := range events {
		if event.Type == EventRunning && event.Role == RolePlanner {
			foundPlannerRunning = true
		}
		if event.Role == RoleCoder {
			t.Fatalf("expected no coder events in no-coder strategy, got %+v", event)
		}
	}
	if !foundPlannerRunning {
		t.Fatal("expected planner execution event in no-coder strategy")
	}
}

func TestOrchestratorNoCoderFileCreationWritesToDisk(t *testing.T) {
	t.Parallel()
	workDir := t.TempDir()

	plannerProv := &toolLoopProvider{}
	events := make(chan AgentEvent, 128)
	orc := &Orchestrator{
		Planner:      newTestAgent(RolePlanner, plannerProv),
		UpdateChan:   make(chan StepUpdate, 64),
		EventChan:    events,
		WorkingDir:   workDir,
		ProjectBrief: "Working directory: .",
		Session:      &state.Session{ExecutionMode: state.ExecutionModeFast},
	}

	if err := orc.Run(context.Background(), "create hamid.ts with basic functions"); err != nil {
		t.Fatalf("run: %v", err)
	}

	target := filepath.Join(workDir, "hamid.ts")
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("expected file to be created at %s: %v", target, err)
	}
	want := "export function hamid(): string {\n\treturn \"hamid\"\n}\n"
	if strings.TrimSpace(string(content)) != strings.TrimSpace(want) {
		t.Fatalf("unexpected created file content:\nwant:\n%s\ngot:\n%s", want, string(content))
	}
	if !plannerProv.sawToolResult {
		t.Fatal("expected tool result payload to be returned back to the model")
	}
	close(events)
	foundWrite := false
	for event := range events {
		if event.Role == RoleCoder {
			t.Fatalf("expected no coder events in planner-solo strategy, got %+v", event)
		}
		if event.Type == EventWriting && event.Role == RolePlanner {
			if payload, ok := event.Payload.(map[string]any); ok {
				if rawPath, hasPath := payload["path"]; hasPath {
					if path, ok := rawPath.(string); ok && strings.TrimSpace(path) == "hamid.ts" {
						foundWrite = true
					}
				}
			}
		}
	}
	if !foundWrite {
		t.Fatal("expected planner write event for hamid.ts")
	}
}

func TestOrchestratorDetectsMissingFileAndFailsAfterVerificationRetries(t *testing.T) {
	t.Parallel()
	workDir := t.TempDir()

	plannerProv := &sequenceProvider{
		replies: []string{
			`{"status":"done"}`,
			`{"status":"done"}`,
			`{"status":"done"}`,
		},
	}

	orc := &Orchestrator{
		Planner:      newTestAgent(RolePlanner, plannerProv),
		UpdateChan:   make(chan StepUpdate, 64),
		WorkingDir:   workDir,
		ProjectBrief: "Working directory: .",
		Session:      &state.Session{ExecutionMode: state.ExecutionModeFast},
	}

	err := orc.Run(context.Background(), "create ghost.ts")
	if err == nil {
		t.Fatal("expected missing-side-effect verification to fail")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "missing on disk") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(workDir, "ghost.ts")); !os.IsNotExist(statErr) {
		t.Fatalf("expected ghost.ts to remain missing, stat err=%v", statErr)
	}
	if got := len(plannerProv.prompts); got < 3 {
		t.Fatalf("expected planner retries with verification prompts, got %d prompt(s)", got)
	}
	lastPrompt := plannerProv.prompts[len(plannerProv.prompts)-1]
	if !strings.Contains(lastPrompt, "You must call the write_file tool") {
		t.Fatalf("expected retry prompt to enforce write_file usage, got %q", lastPrompt)
	}
}

func TestOrchestratorFastModeSkipsPlanFilesAndApproval(t *testing.T) {
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

	updates := make(chan StepUpdate, 64)
	orc := &Orchestrator{
		Planner:      newTestAgent(RolePlanner, plannerProv),
		Coder:        newTestAgent(RoleCoder, coderProv),
		Reviewer:     newTestAgent(RoleReviewer, reviewerProv),
		UpdateChan:   updates,
		WorkingDir:   workDir,
		ProjectBrief: "Working directory: .",
		Session:      &state.Session{ExecutionMode: state.ExecutionModeFast},
	}

	if err := orc.Run(context.Background(), "implement feature"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(coderProv.prompts) == 0 {
		t.Fatal("expected immediate execution dispatch in FAST mode")
	}
	if len(plannerProv.prompts) != 0 {
		t.Fatalf("expected planner to be bypassed in FAST mode when coder is configured, got %d prompt(s)", len(plannerProv.prompts))
	}
	if _, err := os.Stat(filepath.Join(workDir, ".orchestra", "plans")); !os.IsNotExist(err) {
		t.Fatalf("expected FAST mode to skip .orchestra/plans files, stat err=%v", err)
	}

	close(updates)
	for up := range updates {
		if strings.EqualFold(up.Status, "plan_ready") {
			t.Fatalf("did not expect plan_ready gate in FAST mode, got %+v", up)
		}
	}
}

func TestOrchestratorPlanLockWriteIsAsyncAndNonBlocking(t *testing.T) {
	t.Parallel()
	workDir := t.TempDir()

	plannerProv := &sequenceProvider{
		replies: []string{"```yaml\ntasks:\n  - id: t1\n    description: implement feature\n```"},
	}
	coderProv := &planFileReadProvider{}
	reviewerProv := &sequenceProvider{
		replies: []string{`{"approved": true, "findings": []}`},
	}

	orc := &Orchestrator{
		Planner:      newTestAgent(RolePlanner, plannerProv),
		Coder:        newTestAgent(RoleCoder, coderProv),
		Reviewer:     newTestAgent(RoleReviewer, reviewerProv),
		UpdateChan:   make(chan StepUpdate, 128),
		EventChan:    make(chan AgentEvent, 128),
		WorkingDir:   workDir,
		ProjectBrief: "Working directory: .",
		Session:      &state.Session{ExecutionMode: state.ExecutionModePlan},
		writePlanLockFn: func(ctx context.Context, planID string) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}

	start := time.Now()
	if err := orc.Run(context.Background(), "implement feature"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("expected Run to return immediately without waiting on lock write, elapsed=%s", elapsed)
	}
}

func TestOrchestratorSuppressesInternalOrchestraEventsAndUpdates(t *testing.T) {
	t.Parallel()
	workDir := t.TempDir()

	plannerProv := &sequenceProvider{
		replies: []string{"```yaml\ntasks:\n  - id: t1\n    description: implement feature\n```"},
	}
	coderProv := &planFileReadProvider{}
	reviewerProv := &sequenceProvider{
		replies: []string{`{"approved": true, "findings": []}`},
	}
	updates := make(chan StepUpdate, 256)
	events := make(chan AgentEvent, 256)
	orc := &Orchestrator{
		Planner:      newTestAgent(RolePlanner, plannerProv),
		Coder:        newTestAgent(RoleCoder, coderProv),
		Reviewer:     newTestAgent(RoleReviewer, reviewerProv),
		UpdateChan:   updates,
		EventChan:    events,
		WorkingDir:   workDir,
		ProjectBrief: "Working directory: .",
		Session:      &state.Session{ExecutionMode: state.ExecutionModePlan},
	}

	if err := orc.Run(context.Background(), "implement feature"); err != nil {
		t.Fatalf("run: %v", err)
	}
	time.Sleep(150 * time.Millisecond)

	for {
		select {
		case up := <-updates:
			if strings.Contains(strings.ToLower(up.Msg), ".orchestra/") {
				t.Fatalf("unexpected internal .orchestra update leaked to UI channel: %+v", up)
			}
			if strings.Contains(strings.ToLower(up.PlanYAML), ".orchestra/") {
				t.Fatalf("unexpected internal .orchestra plan yaml leaked to UI channel: %+v", up)
			}
		default:
			goto eventsCheck
		}
	}

eventsCheck:
	for {
		select {
		case ev := <-events:
			if eventContainsInternalOrchestraPath(ev) {
				t.Fatalf("unexpected internal .orchestra event leaked to UI channel: %+v", ev)
			}
			if strings.Contains(strings.ToLower(ev.Detail), "plan locked") {
				t.Fatalf("unexpected plan lock event leaked to UI channel: %+v", ev)
			}
		default:
			return
		}
	}
}

func TestOrchestratorRequiresPlannerModel(t *testing.T) {
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

	err := orc.Run(context.Background(), "implement feature")
	if err == nil {
		t.Fatal("expected planner-required error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "planner model is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOrchestratorConversationalBypassesPlanning(t *testing.T) {
	t.Parallel()
	workDir := t.TempDir()

	plannerProv := &sequenceProvider{
		replies: []string{"hello back"},
	}
	events := make(chan AgentEvent, 16)
	orc := &Orchestrator{
		Planner:    newTestAgent(RolePlanner, plannerProv),
		UpdateChan: make(chan StepUpdate, 16),
		EventChan:  events,
		WorkingDir: workDir,
		Session:    &state.Session{ExecutionMode: state.ExecutionModeFast},
	}

	if err := orc.Run(context.Background(), "hi"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(plannerProv.prompts) == 0 || !strings.Contains(strings.ToLower(plannerProv.prompts[0]), "hi") {
		t.Fatalf("expected conversational prompt to reach planner, got %#v", plannerProv.prompts)
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
