package agent

import (
	"context"
	"os"
	"path/filepath"
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
