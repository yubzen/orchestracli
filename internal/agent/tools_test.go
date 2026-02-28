package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultToolSetForRole(t *testing.T) {
	t.Parallel()

	planner := DefaultToolSetForRole(RolePlanner, ToolEnv{WorkingDir: t.TempDir(), Role: RolePlanner})
	if _, ok := planner.Get("write_plan_md"); !ok {
		t.Fatal("planner should have write_plan_md")
	}
	if _, ok := planner.Get("write_file"); ok {
		t.Fatal("planner should not have write_file")
	}

	coder := DefaultToolSetForRole(RoleCoder, ToolEnv{WorkingDir: t.TempDir(), Role: RoleCoder})
	if _, ok := coder.Get("write_file"); !ok {
		t.Fatal("coder should have write_file")
	}
}

func TestWritePlanToolScope(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	toolSet := DefaultToolSetForRole(RolePlanner, ToolEnv{WorkingDir: root, Role: RolePlanner})
	tool, ok := toolSet.Get("write_plan_md")
	if !ok {
		t.Fatal("expected write_plan_md tool")
	}

	ctx := context.Background()
	if _, err := tool.Execute(ctx, map[string]any{
		"path":    "README.md",
		"content": "bad",
	}); err == nil {
		t.Fatal("expected write_plan_md to reject non plan path")
	}

	_, err := tool.Execute(ctx, map[string]any{
		"path":    ".orchestra/plans/task_001.md",
		"content": "# plan",
	})
	if err != nil {
		t.Fatalf("expected scoped plan write to succeed: %v", err)
	}

	outPath := filepath.Join(root, ".orchestra", "plans", "task_001.md")
	content, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read plan file: %v", err)
	}
	if string(content) != "# plan" {
		t.Fatalf("unexpected plan content: %q", string(content))
	}
}

func TestReadOnlyRunCommandTool(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	toolSet := DefaultToolSetForRole(RoleAnalyst, ToolEnv{WorkingDir: root, Role: RoleAnalyst})
	tool, ok := toolSet.Get("run_command")
	if !ok {
		t.Fatal("expected run_command tool")
	}

	if _, err := tool.Execute(context.Background(), map[string]any{
		"command": "echo hello > file.txt",
	}); err == nil {
		t.Fatal("expected read-only command restriction to reject write redirect")
	}
}

func TestWriteFileToolEmitsFileDiffEvent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	events := make([]AgentEvent, 0, 4)
	toolSet := DefaultToolSetForRole(RoleCoder, ToolEnv{
		WorkingDir: root,
		Role:       RoleCoder,
		Emit: func(event AgentEvent) {
			events = append(events, event)
		},
	})
	tool, ok := toolSet.Get("write_file")
	if !ok {
		t.Fatal("expected write_file tool")
	}

	_, err := tool.Execute(context.Background(), map[string]any{
		"path":    "hamid.ts",
		"content": "export const a = 1\n",
	})
	if err != nil {
		t.Fatalf("first write failed: %v", err)
	}
	_, err = tool.Execute(context.Background(), map[string]any{
		"path":    "hamid.ts",
		"content": "export const a = 2\n",
	})
	if err != nil {
		t.Fatalf("second write failed: %v", err)
	}

	foundDiff := false
	for _, event := range events {
		if event.Type != EventFileDiff {
			continue
		}
		payload, ok := event.Payload.(FileDiffPayload)
		if !ok {
			t.Fatalf("expected file diff payload type, got %T", event.Payload)
		}
		if payload.Path != "hamid.ts" {
			continue
		}
		oldJoined := strings.Join(payload.OldLines, "\n")
		newJoined := strings.Join(payload.NewLines, "\n")
		if strings.Contains(oldJoined, "a = 1") && strings.Contains(newJoined, "a = 2") {
			foundDiff = true
			break
		}
	}
	if !foundDiff {
		t.Fatalf("expected EventFileDiff for hamid.ts, got events: %#v", events)
	}
}

func TestWriteFileToolSkipsDiffEventsForOrchestraInternalPaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	events := make([]AgentEvent, 0, 4)
	toolSet := DefaultToolSetForRole(RoleCoder, ToolEnv{
		WorkingDir: root,
		Role:       RoleCoder,
		Emit: func(event AgentEvent) {
			events = append(events, event)
		},
	})
	tool, ok := toolSet.Get("write_file")
	if !ok {
		t.Fatal("expected write_file tool")
	}

	_, err := tool.Execute(context.Background(), map[string]any{
		"path":    ".orchestra/plans/task_001.md",
		"content": "# internal plan\n",
	})
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}

	for _, event := range events {
		if event.Type == EventFileDiff {
			t.Fatalf("expected no diff event for internal .orchestra path, got %+v", event)
		}
	}
}
