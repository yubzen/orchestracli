package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/yubzen/orchestra/internal/rag"
	"github.com/yubzen/orchestra/internal/state"
)

type StepUpdate struct {
	StepID   string
	Status   string // pending, running, done, failed, blocked, plan_ready
	Msg      string
	PlanID   string
	PlanYAML string
}

type PlanTask struct {
	ID            string   `yaml:"id"`
	Description   string   `yaml:"description"`
	FilesToModify []string `yaml:"files_to_modify"`
	FilesToCreate []string `yaml:"files_to_create"`
	DependsOn     []string `yaml:"depends_on"`
}

type YAMLPlan struct {
	Tasks []PlanTask `yaml:"tasks"`
}

type ReviewResult struct {
	Approved bool `json:"approved"`
	Findings []struct {
		File        string `json:"file"`
		Line        int    `json:"line"`
		Severity    string `json:"severity"`
		Description string `json:"description"`
	} `json:"findings"`
}

type ExecutionStrategy int

const (
	StrategyFull ExecutionStrategy = iota
	StrategyNoCoder
	StrategyNoPlanner
	StrategyNoReviewer
	StrategySolo
)

type PlanApproval struct {
	PlanID     string
	Approved   bool
	EditedPlan string
}

type roleAvailability struct {
	planner  bool
	coder    bool
	reviewer bool
}

func (r roleAvailability) any() bool {
	return r.planner || r.coder || r.reviewer
}

type Orchestrator struct {
	Planner          *Agent
	Coder            *Agent
	Reviewer         *Agent
	DB               *state.DB
	Session          *state.Session
	UpdateChan       chan StepUpdate
	EventChan        chan AgentEvent
	PlanApprovalChan chan PlanApproval
	WorkingDir       string
	ProjectBrief     string
}

var ErrOrchestratorNotReady = errors.New("orchestrator is not initialized")

func (o *Orchestrator) emit(update StepUpdate) {
	if o == nil || o.UpdateChan == nil {
		return
	}
	o.UpdateChan <- update
}

func (o *Orchestrator) emitEvent(event AgentEvent) {
	if o == nil || o.EventChan == nil {
		return
	}
	if event.At.IsZero() {
		event.At = time.Now()
	}
	o.EventChan <- event
}

func (o *Orchestrator) Run(ctx context.Context, prompt string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		err := errors.New("prompt is empty")
		o.emit(StepUpdate{StepID: "orchestrator", Status: "failed", Msg: err.Error()})
		return err
	}
	if o == nil {
		return ErrOrchestratorNotReady
	}

	o.bindAgentToolSets()
	if !isTaskMessage(prompt) {
		return o.runConversational(ctx, prompt)
	}
	availability := o.collectAvailability(ctx)
	strategy, err := deriveStrategy(availability)
	if err != nil {
		o.emit(StepUpdate{StepID: "orchestrator", Status: "failed", Msg: err.Error()})
		o.emitEvent(AgentEvent{Type: EventError, Role: RolePlanner, Detail: err.Error()})
		return err
	}

	projectBrief := o.ensureProjectBrief()
	o.emitEvent(AgentEvent{Type: EventPlanning, Role: RolePlanner, Detail: fmt.Sprintf("strategy=%s mode=%s", strategyName(strategy), o.executionMode())})
	o.emit(StepUpdate{
		StepID:   "orchestrator",
		Status:   "running",
		Msg:      fmt.Sprintf("Strategy: %s | mode: %s", strategyName(strategy), o.executionMode()),
		PlanYAML: "",
	})

	planYAML, plan, err := o.buildPlan(ctx, prompt, projectBrief, strategy, availability)
	if err != nil {
		o.emit(StepUpdate{StepID: "planner", Status: "failed", Msg: err.Error()})
		o.emitEvent(AgentEvent{Type: EventError, Role: RolePlanner, Detail: err.Error()})
		return err
	}
	planID := o.newPlanID()
	planPath, err := o.persistPlanMarkdown(ctx, planID, prompt, plan, planYAML)
	if err != nil {
		o.emit(StepUpdate{StepID: "planner", Status: "failed", Msg: err.Error()})
		o.emitEvent(AgentEvent{Type: EventError, Role: RolePlanner, Detail: err.Error()})
		return err
	}
	o.emit(StepUpdate{StepID: "planner", Status: "done", Msg: fmt.Sprintf("Plan saved to %s", planPath)})

	if o.executionMode() == state.ExecutionModePlan {
		approvedPlanYAML, err := o.waitForPlanApproval(ctx, planID, planYAML)
		if err != nil {
			o.emit(StepUpdate{StepID: "planner", Status: "failed", Msg: err.Error()})
			o.emitEvent(AgentEvent{Type: EventError, Role: RolePlanner, Detail: err.Error()})
			return err
		}
		if strings.TrimSpace(approvedPlanYAML) != strings.TrimSpace(planYAML) {
			plan, err = parseYAMLPlan(approvedPlanYAML)
			if err != nil {
				err = fmt.Errorf("edited plan is invalid: %w", err)
				o.emit(StepUpdate{StepID: "planner", Status: "failed", Msg: err.Error()})
				o.emitEvent(AgentEvent{Type: EventError, Role: RolePlanner, Detail: err.Error()})
				return err
			}
			planYAML = approvedPlanYAML
			planPath, err = o.persistPlanMarkdown(ctx, planID, prompt, plan, planYAML)
			if err != nil {
				o.emit(StepUpdate{StepID: "planner", Status: "failed", Msg: err.Error()})
				o.emitEvent(AgentEvent{Type: EventError, Role: RolePlanner, Detail: err.Error()})
				return err
			}
			o.emit(StepUpdate{StepID: "planner", Status: "done", Msg: "Edited plan approved"})
		}
	}

	if strategy == StrategyNoCoder {
		if err := o.runAnalysisOnly(ctx, prompt, projectBrief, planYAML, availability); err != nil {
			o.emit(StepUpdate{StepID: "analysis", Status: "failed", Msg: err.Error()})
			o.emitEvent(AgentEvent{Type: EventError, Role: RoleAnalyst, Detail: err.Error()})
			return err
		}
		o.emit(StepUpdate{StepID: "orchestrator", Status: "done", Msg: "Analysis complete"})
		o.emitEvent(AgentEvent{Type: EventDone, Role: RoleAnalyst, Detail: "analysis complete"})
		return nil
	}

	executor := o.selectExecutor(availability)
	if executor == nil {
		err := errors.New("no execution agent available")
		o.emit(StepUpdate{StepID: "orchestrator", Status: "failed", Msg: err.Error()})
		o.emitEvent(AgentEvent{Type: EventError, Role: RoleCoder, Detail: err.Error()})
		return err
	}
	reviewer := o.selectReviewer(availability, executor)

	if err := o.executePlan(ctx, prompt, projectBrief, plan, planPath, strategy, executor, reviewer); err != nil {
		o.emit(StepUpdate{StepID: "orchestrator", Status: "failed", Msg: err.Error()})
		o.emitEvent(AgentEvent{Type: EventError, Role: RoleCoder, Detail: err.Error()})
		return err
	}
	if err := o.writePlanLock(ctx, planID); err != nil {
		o.emit(StepUpdate{StepID: "planner", Status: "failed", Msg: err.Error()})
		o.emitEvent(AgentEvent{Type: EventError, Role: RolePlanner, Detail: err.Error()})
		return err
	}

	o.emit(StepUpdate{StepID: "orchestrator", Status: "done", Msg: "All tasks completed"})
	o.emitEvent(AgentEvent{Type: EventDone, Role: RoleCoder, Detail: "all tasks completed"})
	return nil
}

func (o *Orchestrator) SubmitPlanApproval(decision PlanApproval) {
	if o == nil || o.PlanApprovalChan == nil {
		return
	}
	select {
	case o.PlanApprovalChan <- decision:
	default:
		select {
		case <-o.PlanApprovalChan:
		default:
		}
		select {
		case o.PlanApprovalChan <- decision:
		default:
		}
	}
}

func (o *Orchestrator) executionMode() string {
	if o == nil || o.Session == nil {
		return state.ExecutionModeFast
	}
	return state.NormalizeExecutionMode(o.Session.ExecutionMode)
}

func (o *Orchestrator) ensureProjectBrief() string {
	if strings.TrimSpace(o.ProjectBrief) != "" {
		return o.ProjectBrief
	}
	workingDir := strings.TrimSpace(o.WorkingDir)
	if workingDir == "" && o.Session != nil {
		workingDir = strings.TrimSpace(o.Session.WorkingDir)
	}
	if workingDir == "" {
		workingDir = "."
	}
	brief, err := rag.BuildProjectBrief(workingDir)
	if err != nil {
		o.ProjectBrief = fmt.Sprintf("Working directory: %s", workingDir)
		return o.ProjectBrief
	}
	o.ProjectBrief = brief
	return brief
}

func (o *Orchestrator) collectAvailability(ctx context.Context) roleAvailability {
	return roleAvailability{
		planner:  o.agentReady(ctx, o.Planner),
		coder:    o.agentReady(ctx, o.Coder),
		reviewer: o.agentReady(ctx, o.Reviewer),
	}
}

func (o *Orchestrator) agentReady(ctx context.Context, a *Agent) bool {
	if a == nil {
		return false
	}
	if err := a.Validate(); err != nil {
		return false
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return a.Provider.Ping(pingCtx) == nil
}

func deriveStrategy(av roleAvailability) (ExecutionStrategy, error) {
	if av.planner && av.coder && av.reviewer {
		return StrategyFull, nil
	}
	if av.planner && !av.coder && av.reviewer {
		return StrategyNoCoder, nil
	}
	if !av.planner && av.coder && av.reviewer {
		return StrategyNoPlanner, nil
	}
	if av.planner && av.coder && !av.reviewer {
		return StrategyNoReviewer, nil
	}
	if av.any() {
		return StrategySolo, nil
	}
	return StrategySolo, errors.New("no available agents; configure at least one working role")
}

func strategyName(s ExecutionStrategy) string {
	switch s {
	case StrategyFull:
		return "full"
	case StrategyNoCoder:
		return "no-coder"
	case StrategyNoPlanner:
		return "no-planner"
	case StrategyNoReviewer:
		return "no-reviewer"
	case StrategySolo:
		return "solo"
	default:
		return "unknown"
	}
}

func (o *Orchestrator) buildPlan(ctx context.Context, prompt, projectBrief string, strategy ExecutionStrategy, availability roleAvailability) (string, YAMLPlan, error) {
	if availability.planner && o.Planner != nil && strategy != StrategyNoPlanner {
		var planYAML string
		var parsed YAMLPlan
		var planErr error

		for attempt := 1; attempt <= 3; attempt++ {
			o.emit(StepUpdate{StepID: "planner", Status: "running", Msg: fmt.Sprintf("Generating plan (attempt %d/3)", attempt)})
			o.emitEvent(AgentEvent{
				Type:   EventThinking,
				Role:   RolePlanner,
				Detail: fmt.Sprintf("Planner is drafting execution plan (attempt %d/3)", attempt),
			})
			plannerPrompt := o.buildPlannerPrompt(projectBrief, prompt)
			planYAML, planErr = o.Planner.RunWithOptions(ctx, plannerPrompt, o.Session, o.DB, RunOptions{
				Mode:    DispatchModeTask,
				OnToken: o.streamTokenCallback(RolePlanner),
			})
			if planErr != nil {
				continue
			}

			parsed, planErr = parseYAMLPlan(planYAML)
			if planErr == nil && len(parsed.Tasks) > 0 {
				o.emit(StepUpdate{StepID: "planner", Status: "done", Msg: fmt.Sprintf("Plan generated with %d task(s)", len(parsed.Tasks))})
				o.emitEvent(AgentEvent{Type: EventDone, Role: RolePlanner, Detail: fmt.Sprintf("plan generated with %d task(s)", len(parsed.Tasks))})
				return planYAML, parsed, nil
			}
		}
		if planErr == nil {
			planErr = errors.New("planner returned empty plan")
		}
		return "", YAMLPlan{}, fmt.Errorf("planner failed: %w", planErr)
	}

	fallback := fallbackPlan(prompt)
	fallbackYAML := renderPlanYAML(fallback)
	o.emit(StepUpdate{StepID: "planner", Status: "done", Msg: "Planner unavailable; using fallback single-task plan"})
	o.emitEvent(AgentEvent{Type: EventDone, Role: RolePlanner, Detail: "planner unavailable; using fallback plan"})
	return fallbackYAML, fallback, nil
}

func (o *Orchestrator) waitForPlanApproval(ctx context.Context, planID, planYAML string) (string, error) {
	planID = strings.TrimSpace(planID)
	if planID == "" {
		planID = o.newPlanID()
	}
	o.emit(StepUpdate{
		StepID:   "planner",
		Status:   "plan_ready",
		Msg:      "Plan is ready for review. Approve, edit, or reject.",
		PlanID:   planID,
		PlanYAML: strings.TrimSpace(planYAML),
	})
	o.emitEvent(AgentEvent{
		Type:   EventWaiting,
		Role:   RolePlanner,
		Detail: "waiting for plan approval",
		Payload: map[string]any{
			"plan_id": planID,
		},
	})

	if o.PlanApprovalChan == nil {
		o.emit(StepUpdate{StepID: "planner", Status: "done", Msg: "Plan auto-approved (no approval channel configured)"})
		o.emitEvent(AgentEvent{Type: EventDone, Role: RolePlanner, Detail: "plan auto-approved"})
		return planYAML, nil
	}

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case decision := <-o.PlanApprovalChan:
			if strings.TrimSpace(decision.PlanID) != planID {
				continue
			}
			if !decision.Approved {
				o.emitEvent(AgentEvent{Type: EventError, Role: RolePlanner, Detail: "plan rejected"})
				return "", errors.New("plan rejected by user")
			}
			edited := strings.TrimSpace(decision.EditedPlan)
			if edited != "" {
				o.emitEvent(AgentEvent{Type: EventDone, Role: RolePlanner, Detail: "edited plan approved"})
				return edited, nil
			}
			o.emitEvent(AgentEvent{Type: EventDone, Role: RolePlanner, Detail: "plan approved"})
			return planYAML, nil
		}
	}
}

func (o *Orchestrator) runAnalysisOnly(ctx context.Context, prompt, projectBrief, planYAML string, availability roleAvailability) error {
	analyst := o.Planner
	if analyst == nil || !availability.planner {
		analyst = o.Reviewer
	}
	if analyst == nil || !o.agentReady(ctx, analyst) {
		return errors.New("analysis strategy selected but no planner/reviewer is available")
	}

	analysisPrompt := strings.TrimSpace(fmt.Sprintf(`
Project context:
%s

User request:
%s

Execution plan:
%s

There is no coder available. Provide a concise implementation strategy, risks, and validation checklist.
	`, projectBrief, prompt, planYAML))
	o.emit(StepUpdate{StepID: "analysis", Status: "running", Msg: "Generating implementation strategy without coder"})
	o.emitEvent(AgentEvent{Type: EventThinking, Role: analyst.Role, Detail: "analysis-only mode: generating implementation guidance"})
	_, err := analyst.RunWithOptions(ctx, analysisPrompt, o.Session, o.DB, RunOptions{
		Mode:    DispatchModeTask,
		OnToken: o.streamTokenCallback(analyst.Role),
	})
	if err != nil {
		return err
	}
	o.emit(StepUpdate{StepID: "analysis", Status: "done", Msg: "Analysis completed"})
	o.emitEvent(AgentEvent{Type: EventDone, Role: analyst.Role, Detail: "analysis completed"})
	return nil
}

func (o *Orchestrator) selectExecutor(availability roleAvailability) *Agent {
	if availability.coder && o.Coder != nil {
		return o.Coder
	}
	if availability.planner && o.Planner != nil {
		return o.Planner
	}
	if availability.reviewer && o.Reviewer != nil {
		return o.Reviewer
	}
	return nil
}

func (o *Orchestrator) selectReviewer(availability roleAvailability, executor *Agent) *Agent {
	if !availability.reviewer || o.Reviewer == nil {
		return nil
	}
	if executor == nil {
		return o.Reviewer
	}
	if executor == o.Reviewer {
		return nil
	}
	return o.Reviewer
}

func (o *Orchestrator) executePlan(ctx context.Context, prompt, projectBrief string, plan YAMLPlan, planPath string, strategy ExecutionStrategy, executor, reviewer *Agent) error {
	if len(plan.Tasks) == 0 {
		return errors.New("execution plan has no tasks")
	}

	completed := make(map[string]bool, len(plan.Tasks))
	for len(completed) < len(plan.Tasks) {
		progress := false
		for _, task := range plan.Tasks {
			if completed[task.ID] {
				continue
			}
			if !depsSatisfied(task.DependsOn, completed) {
				continue
			}
			progress = true
			if err := o.runTask(ctx, prompt, projectBrief, task, planPath, strategy, executor, reviewer); err != nil {
				return err
			}
			completed[task.ID] = true
		}
		if !progress {
			return errors.New("plan dependencies cannot be resolved (deadlock detected)")
		}
	}
	return nil
}

func (o *Orchestrator) runTask(ctx context.Context, prompt, projectBrief string, task PlanTask, planPath string, strategy ExecutionStrategy, executor, reviewer *Agent) error {
	if executor == nil {
		return errors.New("no executor agent available")
	}

	basePrompt := o.buildTaskPrompt(projectBrief, prompt, task, strategy, "")
	o.emit(StepUpdate{StepID: task.ID, Status: "running", Msg: "Executing task"})
	o.emitEvent(AgentEvent{
		Type:   EventRunning,
		Role:   executor.Role,
		Detail: fmt.Sprintf("executing %s", task.ID),
	})

	taskPrompt := basePrompt
	rolePrompt := strings.ToUpper(strings.TrimSpace(string(executor.Role)))
	if rolePrompt == "" {
		rolePrompt = "CODER"
	}
	fileContext := o.buildTaskFileContext(ctx, executor, task)
	if fileContext != "" {
		taskPrompt = strings.TrimSpace(taskPrompt + "\n\nRelevant file contents:\n" + fileContext)
	}
	for attempt := 1; attempt <= 3; attempt++ {
		o.emitEvent(AgentEvent{
			Type:   EventThinking,
			Role:   executor.Role,
			Detail: fmt.Sprintf("%s is thinking about %s (attempt %d/3)", rolePrompt, task.ID, attempt),
		})
		coderOut, err := executor.RunWithOptions(ctx, taskPrompt, o.Session, o.DB, RunOptions{
			Mode:    DispatchModeTask,
			OnToken: o.streamTokenCallback(executor.Role),
		})
		if err != nil {
			if attempt == 3 {
				o.emit(StepUpdate{StepID: task.ID, Status: "blocked", Msg: "Executor failed after retries"})
				o.emitEvent(AgentEvent{Type: EventError, Role: executor.Role, Detail: fmt.Sprintf("execution failed for %s", task.ID)})
				return err
			}
			continue
		}

		if reviewer == nil {
			o.emit(StepUpdate{StepID: task.ID, Status: "done", Msg: "Task completed (no reviewer configured)"})
			if err := o.updatePlanTaskStatus(ctx, planPath, task.ID, true); err != nil {
				o.emitEvent(AgentEvent{Type: EventError, Role: RolePlanner, Detail: err.Error()})
			}
			o.emitEvent(AgentEvent{Type: EventDone, Role: executor.Role, Detail: fmt.Sprintf("task %s complete", task.ID)})
			return nil
		}

		o.emit(StepUpdate{StepID: task.ID, Status: "running", Msg: "Reviewing task output"})
		o.emitEvent(AgentEvent{
			Type:   EventReviewing,
			Role:   reviewer.Role,
			Detail: fmt.Sprintf("reviewing %s", task.ID),
		})
		reviewPrompt := o.buildReviewPrompt(projectBrief, prompt, task, coderOut)
		o.emitEvent(AgentEvent{
			Type:   EventThinking,
			Role:   reviewer.Role,
			Detail: fmt.Sprintf("%s analyzing %s (attempt %d/3)", strings.ToUpper(strings.TrimSpace(string(reviewer.Role))), task.ID, attempt),
		})
		reviewOut, err := reviewer.RunWithOptions(ctx, reviewPrompt, o.Session, o.DB, RunOptions{
			Mode:    DispatchModeTask,
			OnToken: o.streamTokenCallback(reviewer.Role),
		})
		if err != nil {
			if attempt == 3 {
				o.emit(StepUpdate{StepID: task.ID, Status: "blocked", Msg: "Reviewer failed after retries"})
				o.emitEvent(AgentEvent{Type: EventError, Role: reviewer.Role, Detail: fmt.Sprintf("review failed for %s", task.ID)})
				return err
			}
			continue
		}

		approved, findings := parseReviewDecision(reviewOut)
		if approved {
			o.emit(StepUpdate{StepID: task.ID, Status: "done", Msg: "Approved"})
			if err := o.updatePlanTaskStatus(ctx, planPath, task.ID, true); err != nil {
				o.emitEvent(AgentEvent{Type: EventError, Role: RolePlanner, Detail: err.Error()})
			}
			o.emitEvent(AgentEvent{Type: EventDone, Role: reviewer.Role, Detail: fmt.Sprintf("approved %s", task.ID)})
			return nil
		}

		taskPrompt = o.buildTaskPrompt(projectBrief, prompt, task, strategy, findings)
		if fileContext != "" {
			taskPrompt = strings.TrimSpace(taskPrompt + "\n\nRelevant file contents:\n" + fileContext)
		}
		o.emitEvent(AgentEvent{
			Type:   EventWaiting,
			Role:   executor.Role,
			Detail: fmt.Sprintf("waiting on reviewer findings for %s", task.ID),
		})
	}

	o.emit(StepUpdate{StepID: task.ID, Status: "blocked", Msg: "Review loop exceeded retry budget"})
	o.emitEvent(AgentEvent{Type: EventError, Role: executor.Role, Detail: fmt.Sprintf("retry budget exceeded for %s", task.ID)})
	return fmt.Errorf("task %s blocked after retries", task.ID)
}

func (o *Orchestrator) buildPlannerPrompt(projectBrief, prompt string) string {
	return strings.TrimSpace(fmt.Sprintf(`
Project context:
%s

User request:
%s

You are the Planner. Return ONLY valid YAML with the schema:
tasks:
  - id: short-task-id
    description: concise action
    files_to_modify: [optional, ...]
    files_to_create: [optional, ...]
    depends_on: [optional, ...]
`, projectBrief, prompt))
}

func (o *Orchestrator) buildTaskPrompt(projectBrief, prompt string, task PlanTask, strategy ExecutionStrategy, reviewerFindings string) string {
	var specialInstruction string
	if strategy == StrategyNoPlanner || strategy == StrategySolo {
		specialInstruction = "You have no Planner teammate. Think step by step, state your plan briefly, then implement."
	}
	if strategy == StrategyNoReviewer {
		if specialInstruction != "" {
			specialInstruction += " "
		}
		specialInstruction += "You have no Reviewer teammate. Perform a self-review before finalizing."
	}

	var feedbackBlock string
	if strings.TrimSpace(reviewerFindings) != "" {
		feedbackBlock = "\nReviewer feedback to address before completing:\n" + reviewerFindings + "\n"
	}

	return strings.TrimSpace(fmt.Sprintf(`
Project context:
%s

User request:
%s

Task ID: %s
Task description: %s
Files to modify: %s
Files to create: %s
Depends on: %s

%s
%s
`, projectBrief, prompt, task.ID, task.Description, strings.Join(task.FilesToModify, ", "), strings.Join(task.FilesToCreate, ", "), strings.Join(task.DependsOn, ", "), specialInstruction, feedbackBlock))
}

func (o *Orchestrator) buildReviewPrompt(projectBrief, prompt string, task PlanTask, executorOutput string) string {
	return strings.TrimSpace(fmt.Sprintf(`
Project context:
%s

User request:
%s

Task ID: %s
Task description: %s

Executor output:
%s

You are the Reviewer. Return ONLY JSON with this schema:
{"approved": true|false, "findings":[{"file":"path","line":1,"severity":"critical|high|medium|low","description":"issue"}]}
`, projectBrief, prompt, task.ID, task.Description, executorOutput))
}

func parseYAMLPlan(raw string) (YAMLPlan, error) {
	cleaned := cleanFencedBlock(raw, "yaml")
	var plan YAMLPlan
	if err := yaml.Unmarshal([]byte(cleaned), &plan); err != nil {
		return YAMLPlan{}, err
	}
	normalized := make([]PlanTask, 0, len(plan.Tasks))
	for idx, task := range plan.Tasks {
		task.ID = strings.TrimSpace(task.ID)
		task.Description = strings.TrimSpace(task.Description)
		if task.ID == "" {
			task.ID = fmt.Sprintf("task-%d", idx+1)
		}
		if task.Description == "" {
			task.Description = fmt.Sprintf("Task %d", idx+1)
		}
		normalized = append(normalized, task)
	}
	plan.Tasks = normalized
	if len(plan.Tasks) == 0 {
		return YAMLPlan{}, errors.New("plan has no tasks")
	}
	return plan, nil
}

func fallbackPlan(prompt string) YAMLPlan {
	return YAMLPlan{
		Tasks: []PlanTask{
			{
				ID:          "task-1",
				Description: strings.TrimSpace(prompt),
			},
		},
	}
}

func renderPlanYAML(plan YAMLPlan) string {
	out, err := yaml.Marshal(plan)
	if err != nil {
		return "tasks: []"
	}
	return string(out)
}

func cleanFencedBlock(raw, language string) string {
	raw = strings.TrimSpace(raw)
	language = strings.ToLower(strings.TrimSpace(language))
	prefixes := []string{"```", "~~~"}
	for _, prefix := range prefixes {
		withLang := prefix + language
		if strings.HasPrefix(strings.ToLower(raw), withLang) {
			raw = strings.TrimSpace(raw[len(withLang):])
		} else if strings.HasPrefix(raw, prefix) {
			raw = strings.TrimSpace(strings.TrimPrefix(raw, prefix))
		}
		if strings.HasSuffix(raw, prefix) {
			raw = strings.TrimSpace(strings.TrimSuffix(raw, prefix))
		}
	}
	return strings.TrimSpace(raw)
}

func parseReviewDecision(raw string) (bool, string) {
	cleaned := cleanFencedBlock(raw, "json")
	var rr ReviewResult
	if err := json.Unmarshal([]byte(cleaned), &rr); err == nil {
		if rr.Approved {
			return true, ""
		}
		findings, marshalErr := json.Marshal(rr.Findings)
		if marshalErr != nil {
			return false, cleaned
		}
		return false, string(findings)
	}

	lower := strings.ToLower(cleaned)
	if strings.Contains(lower, `"approved": true`) || strings.Contains(lower, "approved=true") {
		return true, ""
	}
	return false, cleaned
}

func depsSatisfied(deps []string, completed map[string]bool) bool {
	for _, dep := range deps {
		if !completed[strings.TrimSpace(dep)] {
			return false
		}
	}
	return true
}

var planIDUnsafeChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func (o *Orchestrator) bindAgentToolSets() {
	workingDir := o.effectiveWorkingDir()
	if o.Planner != nil {
		o.Planner.BindToolSet(ToolEnv{
			WorkingDir: workingDir,
			Role:       RolePlanner,
			Emit:       o.emitEvent,
		})
	}
	if o.Coder != nil {
		o.Coder.BindToolSet(ToolEnv{
			WorkingDir: workingDir,
			Role:       RoleCoder,
			Emit:       o.emitEvent,
		})
	}
	if o.Reviewer != nil {
		o.Reviewer.BindToolSet(ToolEnv{
			WorkingDir: workingDir,
			Role:       RoleReviewer,
			Emit:       o.emitEvent,
		})
	}
}

func (o *Orchestrator) effectiveWorkingDir() string {
	if o == nil {
		return "."
	}
	workingDir := strings.TrimSpace(o.WorkingDir)
	if workingDir == "" && o.Session != nil {
		workingDir = strings.TrimSpace(o.Session.WorkingDir)
	}
	if workingDir == "" {
		workingDir = "."
	}
	return workingDir
}

func (o *Orchestrator) newPlanID() string {
	return "task_" + time.Now().UTC().Format("20060102_150405")
}

func normalizePlanID(planID string) string {
	planID = strings.TrimSpace(planID)
	if planID == "" {
		return ""
	}
	planID = planIDUnsafeChars.ReplaceAllString(planID, "_")
	return strings.Trim(planID, "_")
}

func (o *Orchestrator) persistPlanMarkdown(ctx context.Context, planID, prompt string, plan YAMLPlan, planYAML string) (string, error) {
	planID = normalizePlanID(planID)
	if planID == "" {
		return "", errors.New("plan id is empty")
	}
	planPath := filepath.ToSlash(filepath.Join(".orchestra", "plans", planID+".md"))
	body := renderPlanMarkdown(planID, prompt, plan, planYAML)
	if _, err := o.writePlanFile(ctx, planPath, body); err != nil {
		return "", err
	}
	return planPath, nil
}

func renderPlanMarkdown(planID, prompt string, plan YAMLPlan, planYAML string) string {
	title := strings.TrimSpace(prompt)
	if title == "" {
		title = "Execution Plan"
	}
	if len([]rune(title)) > 96 {
		runes := []rune(title)
		title = string(runes[:96]) + "..."
	}

	var b strings.Builder
	b.WriteString("# Task: " + title + "\n")
	b.WriteString("**id**: " + strings.TrimSpace(planID) + "\n")
	b.WriteString("**status**: in_progress\n\n")

	b.WriteString("## Steps\n")
	for _, task := range plan.Tasks {
		taskID := strings.TrimSpace(task.ID)
		if taskID == "" {
			continue
		}
		description := strings.TrimSpace(task.Description)
		if description == "" {
			description = taskID
		}
		b.WriteString("- [ ] " + taskID + " | " + description + "\n")
	}
	if len(plan.Tasks) == 0 {
		b.WriteString("- [ ] task-1 | " + strings.TrimSpace(prompt) + "\n")
	}
	b.WriteString("\n## Context\n")
	b.WriteString("Generated by Planner. Update checkboxes as tasks complete.\n\n")
	b.WriteString("## YAML\n```yaml\n")
	b.WriteString(strings.TrimSpace(planYAML))
	b.WriteString("\n```\n")
	return b.String()
}

func (o *Orchestrator) writePlanFile(ctx context.Context, planPath, content string) (string, error) {
	params := map[string]any{
		"path":    strings.TrimSpace(planPath),
		"content": content,
	}
	if o != nil && o.Planner != nil {
		if _, ok := o.Planner.ToolSet.Get("write_plan_md"); ok {
			if _, err := o.Planner.ExecuteTool(ctx, "write_plan_md", params); err == nil {
				return strings.TrimSpace(planPath), nil
			}
		}
	}

	toolSet := DefaultToolSetForRole(RolePlanner, ToolEnv{
		WorkingDir: o.effectiveWorkingDir(),
		Role:       RolePlanner,
		Emit:       o.emitEvent,
	})
	tool, ok := toolSet.Get("write_plan_md")
	if !ok || tool.Execute == nil {
		return "", errors.New("planner write_plan_md tool is unavailable")
	}
	if _, err := tool.Execute(ctx, params); err != nil {
		return "", err
	}
	return strings.TrimSpace(planPath), nil
}

func (o *Orchestrator) updatePlanTaskStatus(ctx context.Context, planPath, taskID string, done bool) error {
	planPath = strings.TrimSpace(planPath)
	taskID = strings.TrimSpace(taskID)
	if planPath == "" || taskID == "" {
		return nil
	}
	workingDir := o.effectiveWorkingDir()
	absPath, _, err := resolveWorkspacePath(workingDir, planPath)
	if err != nil {
		return err
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		return err
	}
	lines := strings.Split(string(content), "\n")
	uncheckedPrefix := "- [ ] " + taskID + " |"
	checkedPrefix := "- [x] " + taskID + " |"
	changed := false
	for i := range lines {
		switch {
		case strings.HasPrefix(lines[i], uncheckedPrefix) && done:
			lines[i] = strings.Replace(lines[i], "- [ ] ", "- [x] ", 1)
			changed = true
		case strings.HasPrefix(lines[i], checkedPrefix) && !done:
			lines[i] = strings.Replace(lines[i], "- [x] ", "- [ ] ", 1)
			changed = true
		}
	}
	if !changed {
		return nil
	}

	updated := strings.Join(lines, "\n")
	if err := os.WriteFile(absPath, []byte(updated), 0o644); err != nil {
		return err
	}
	o.emitEvent(AgentEvent{
		Type:   EventWriting,
		Role:   RolePlanner,
		Detail: fmt.Sprintf("writing %s", planPath),
		Payload: map[string]any{
			"path": planPath,
		},
	})
	return nil
}

func (o *Orchestrator) writePlanLock(ctx context.Context, planID string) error {
	planID = normalizePlanID(planID)
	if planID == "" {
		return nil
	}
	lockPath := filepath.ToSlash(filepath.Join(".orchestra", "plans", planID+".lock"))
	workingDir := o.effectiveWorkingDir()
	absPath, relPath, err := resolveWorkspacePath(workingDir, lockPath)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return err
	}
	lockBody := fmt.Sprintf("status=done\nplan_id=%s\nfinished_at=%s\n", planID, time.Now().UTC().Format(time.RFC3339))
	if err := os.WriteFile(absPath, []byte(lockBody), 0o644); err != nil {
		return err
	}
	o.emitEvent(AgentEvent{
		Type:   EventWriting,
		Role:   RolePlanner,
		Detail: fmt.Sprintf("writing %s", relPath),
		Payload: map[string]any{
			"path": relPath,
		},
	})
	o.emitEvent(AgentEvent{
		Type:   EventDone,
		Role:   RolePlanner,
		Detail: "plan locked",
	})
	_ = ctx
	return nil
}

func (o *Orchestrator) buildTaskFileContext(ctx context.Context, executor *Agent, task PlanTask) string {
	if executor == nil {
		return ""
	}
	if _, ok := executor.ToolSet.Get("read_file"); !ok {
		return ""
	}

	seen := make(map[string]struct{})
	paths := make([]string, 0, len(task.FilesToModify)+len(task.FilesToCreate))
	for _, path := range append(task.FilesToModify, task.FilesToCreate...) {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
		if len(paths) >= 8 {
			break
		}
	}
	if len(paths) == 0 {
		return ""
	}

	var b strings.Builder
	totalChars := 0
	for _, path := range paths {
		result, err := executor.ExecuteTool(ctx, "read_file", map[string]any{"path": path})
		if err != nil {
			continue
		}
		content := strings.TrimSpace(result.Output)
		if content == "" {
			continue
		}
		if len(content) > 2500 {
			content = content[:2500] + "\n... (truncated)"
		}
		section := fmt.Sprintf("---\nFile: %s\n%s\n", path, content)
		if totalChars+len(section) > 10000 {
			break
		}
		b.WriteString(section)
		totalChars += len(section)
	}
	return strings.TrimSpace(b.String())
}

func (o *Orchestrator) streamTokenCallback(role Role) func(token string) {
	return func(token string) {
		if token == "" {
			return
		}
		o.emitEvent(AgentEvent{
			Type:   EventThinking,
			Role:   role,
			Detail: token,
			Payload: map[string]any{
				"token": token,
			},
		})
	}
}

func (o *Orchestrator) runConversational(ctx context.Context, prompt string) error {
	availability := o.collectAvailability(ctx)
	responder := o.selectExecutor(availability)
	if responder == nil {
		err := errors.New("no available agents for conversational response")
		o.emitEvent(AgentEvent{Type: EventError, Role: RoleCoder, Detail: err.Error()})
		o.emit(StepUpdate{StepID: "orchestrator", Status: "failed", Msg: err.Error()})
		return err
	}

	o.emitEvent(AgentEvent{
		Type:   EventThinking,
		Role:   responder.Role,
		Detail: "preparing conversational response",
	})
	reply, err := responder.RunWithOptions(ctx, prompt, o.Session, o.DB, RunOptions{
		Mode:    DispatchModeChat,
		OnToken: o.streamTokenCallback(responder.Role),
	})
	if err != nil {
		o.emitEvent(AgentEvent{Type: EventError, Role: responder.Role, Detail: err.Error()})
		o.emit(StepUpdate{StepID: "orchestrator", Status: "failed", Msg: err.Error()})
		return err
	}
	o.emitEvent(AgentEvent{
		Type:   EventDone,
		Role:   responder.Role,
		Detail: "conversation response complete",
		Payload: map[string]any{
			"reply": reply,
		},
	})
	o.emit(StepUpdate{StepID: "orchestrator", Status: "done", Msg: "Conversational response complete"})
	return nil
}
