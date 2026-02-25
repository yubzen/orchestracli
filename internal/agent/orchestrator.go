package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/yubzen/orchestra/internal/state"
)

type StepUpdate struct {
	StepID string
	Status string // pending, running, done, failed, blocked
	Msg    string
}

type YAMLPlan struct {
	Tasks []struct {
		ID            string   `yaml:"id"`
		Description   string   `yaml:"description"`
		FilesToModify []string `yaml:"files_to_modify"`
		FilesToCreate []string `yaml:"files_to_create"`
		DependsOn     []string `yaml:"depends_on"`
	} `yaml:"tasks"`
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

type Orchestrator struct {
	Planner    *Agent
	Coder      *Agent
	Reviewer   *Agent
	DB         *state.DB
	Session    *state.Session
	UpdateChan chan StepUpdate
}

var ErrOrchestratorNotReady = errors.New("orchestrator is not initialized")

func (o *Orchestrator) emit(update StepUpdate) {
	if o == nil || o.UpdateChan == nil {
		return
	}
	o.UpdateChan <- update
}

func (o *Orchestrator) validate(ctx context.Context) error {
	if o == nil {
		return ErrOrchestratorNotReady
	}

	agents := []*Agent{o.Planner, o.Coder, o.Reviewer}
	for _, a := range agents {
		if err := a.Validate(); err != nil {
			return err
		}
		if err := a.Provider.Ping(ctx); err != nil {
			return fmt.Errorf("%s agent provider is not ready: %w", a.Role, err)
		}
	}

	return nil
}

func (o *Orchestrator) Run(ctx context.Context, prompt string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := o.validate(ctx); err != nil {
		o.emit(StepUpdate{StepID: "orchestrator", Status: "failed", Msg: err.Error()})
		return err
	}

	var planYAML string
	var parsedPlan YAMLPlan
	var err error

	for i := 0; i < 3; i++ {
		o.emit(StepUpdate{StepID: "planner", Status: "running", Msg: fmt.Sprintf("Generating plan (attempt %d)", i+1)})
		planYAML, err = o.Planner.Run(ctx, prompt, o.Session, o.DB)
		if err == nil {
			// clean yaml marks
			planYAML = strings.TrimPrefix(planYAML, "```yaml")
			planYAML = strings.TrimPrefix(planYAML, "```")
			planYAML = strings.TrimSuffix(planYAML, "```")
			err = yaml.Unmarshal([]byte(planYAML), &parsedPlan)
			if err == nil && len(parsedPlan.Tasks) > 0 {
				o.emit(StepUpdate{StepID: "planner", Status: "done", Msg: "Plan generated"})
				break
			}
		}
	}
	if err != nil || len(parsedPlan.Tasks) == 0 {
		o.emit(StepUpdate{StepID: "planner", Status: "failed", Msg: "Failed to generate valid YAML plan"})
		return fmt.Errorf("planner failed: %v", err)
	}

	var wg sync.WaitGroup
	errChan := make(chan error, len(parsedPlan.Tasks))

	completed := make(map[string]bool)
	var mu sync.Mutex

	runStep := func(taskID, desc string) error {
		o.emit(StepUpdate{StepID: taskID, Status: "running", Msg: "Coding..."})
		input := fmt.Sprintf("Task ID: %s\nDescription: %s", taskID, desc)

		for retry := 0; retry < 3; retry++ {
			coderOut, err := o.Coder.Run(ctx, input, o.Session, o.DB)
			if err != nil {
				if retry == 2 {
					o.emit(StepUpdate{StepID: taskID, Status: "blocked", Msg: "Coder failed"})
					return err
				}
				continue
			}

			o.emit(StepUpdate{StepID: taskID, Status: "running", Msg: "Reviewing..."})
			reviewOut, err := o.Reviewer.Run(ctx, coderOut, o.Session, o.DB)
			if err != nil {
				if retry == 2 {
					o.emit(StepUpdate{StepID: taskID, Status: "blocked", Msg: "Reviewer failed"})
					return err
				}
				continue
			}

			reviewOut = strings.TrimPrefix(reviewOut, "```json")
			reviewOut = strings.TrimPrefix(reviewOut, "```")
			reviewOut = strings.TrimSuffix(reviewOut, "```")

			var rr ReviewResult
			if err := json.Unmarshal([]byte(reviewOut), &rr); err != nil {
				continue
			}

			if rr.Approved {
				o.emit(StepUpdate{StepID: taskID, Status: "done", Msg: "Approved"})
				mu.Lock()
				completed[taskID] = true
				mu.Unlock()
				return nil
			}

			findingsJSON, _ := json.Marshal(rr.Findings)
			input = fmt.Sprintf("Task ID: %s\nDescription: %s\n\nReviewer rejected with findings:\n%s\nPlease fix and resubmit.", taskID, desc, string(findingsJSON))
		}

		o.emit(StepUpdate{StepID: taskID, Status: "blocked", Msg: "Review failed after max retries"})
		return fmt.Errorf("task %s blocked", taskID)
	}

	for _, task := range parsedPlan.Tasks {
		if len(task.DependsOn) == 0 {
			wg.Add(1)
			go func(tID, tDesc string) {
				defer wg.Done()
				err := runStep(tID, tDesc)
				if err != nil {
					errChan <- err
				}
			}(task.ID, task.Description)
		}
	}

	wg.Wait()

	select {
	case err := <-errChan:
		return err
	default:
	}

	for {
		mu.Lock()
		c := len(completed)
		mu.Unlock()
		if c == len(parsedPlan.Tasks) {
			break
		}

		launched := false
		for _, task := range parsedPlan.Tasks {
			mu.Lock()
			isDone := completed[task.ID]
			mu.Unlock()

			if isDone {
				continue
			}

			canRun := true
			for _, dep := range task.DependsOn {
				mu.Lock()
				depDone := completed[dep]
				mu.Unlock()
				if !depDone {
					canRun = false
					break
				}
			}

			if canRun {
				launched = true
				if err := runStep(task.ID, task.Description); err != nil {
					return err
				}
			}
		}

		if !launched {
			return fmt.Errorf("deadlock or missing dependency in plan")
		}
	}

	return nil
}
