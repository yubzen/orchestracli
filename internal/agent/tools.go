package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/yubzen/orchestra/internal/providers"
)

var (
	errToolNotAllowed   = errors.New("tool is not allowed for this role")
	errMissingParameter = errors.New("required tool parameter is missing")
)

type ToolResult struct {
	Output string
	Data   map[string]any
}

type Tool struct {
	Name        string
	Description string
	Execute     func(ctx context.Context, params map[string]any) (ToolResult, error)
}

type ToolSet struct {
	ordered []Tool
	byName  map[string]Tool
}

type ToolEnv struct {
	WorkingDir string
	Role       Role
	Emit       func(AgentEvent)
}

func NewToolSet(tools ...Tool) ToolSet {
	byName := make(map[string]Tool, len(tools))
	ordered := make([]Tool, 0, len(tools))
	for _, t := range tools {
		name := strings.TrimSpace(strings.ToLower(t.Name))
		if name == "" {
			continue
		}
		t.Name = name
		ordered = append(ordered, t)
		byName[name] = t
	}
	return ToolSet{
		ordered: ordered,
		byName:  byName,
	}
}

func (t ToolSet) Get(name string) (Tool, bool) {
	if t.byName == nil {
		return Tool{}, false
	}
	tool, ok := t.byName[strings.ToLower(strings.TrimSpace(name))]
	return tool, ok
}

func (t ToolSet) Names() []string {
	out := make([]string, 0, len(t.ordered))
	for _, tool := range t.ordered {
		out = append(out, tool.Name)
	}
	return out
}

func (t ToolSet) PromptBlock() string {
	if len(t.ordered) == 0 {
		return ""
	}
	lines := make([]string, 0, len(t.ordered)+2)
	lines = append(lines, "Available tools (enforced at runtime):")
	for _, tool := range t.ordered {
		lines = append(lines, fmt.Sprintf("- %s: %s", tool.Name, strings.TrimSpace(tool.Description)))
	}
	lines = append(lines, "If a needed tool is unavailable, explain the limitation and continue with available tools.")
	return strings.Join(lines, "\n")
}

// ProviderTools converts the agent's internal tool set to provider-compatible
// tool definitions with JSON Schema for each tool's parameters.
func (t ToolSet) ProviderTools() []providers.Tool {
	out := make([]providers.Tool, 0, len(t.ordered))
	for _, tool := range t.ordered {
		out = append(out, providers.Tool{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: toolInputSchema(tool.Name),
		})
	}
	return out
}

// toolInputSchema returns the JSON Schema for a tool's input parameters.
func toolInputSchema(name string) map[string]interface{} {
	switch name {
	case "read_file":
		return map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "Relative path to the file within the workspace.",
				},
			},
			"required": []string{"path"},
		}
	case "write_file":
		return map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "Relative path to the file within the workspace.",
				},
				"content": map[string]interface{}{
					"type":        "string",
					"description": "Full content to write to the file.",
				},
			},
			"required": []string{"path", "content"},
		}
	case "write_plan_md":
		return map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "Path to plan file, must be .orchestra/plans/<task_id>.md.",
				},
				"content": map[string]interface{}{
					"type":        "string",
					"description": "Full markdown content for the plan file.",
				},
			},
			"required": []string{"path", "content"},
		}
	case "run_command":
		return map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"command": map[string]interface{}{
					"type":        "string",
					"description": "Shell command to execute in the workspace.",
				},
			},
			"required": []string{"command"},
		}
	default:
		return map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		}
	}
}

func DefaultToolSetForRole(role Role, env ToolEnv) ToolSet {
	if strings.TrimSpace(env.WorkingDir) == "" {
		env.WorkingDir = "."
	}
	switch role {
	case RolePlanner:
		return NewToolSet(
			newReadFileTool(env),
			newWritePlanTool(env),
		)
	case RoleCoder:
		return NewToolSet(
			newReadFileTool(env),
			newWriteFileTool(env),
			newRunCommandTool(env, false),
		)
	case RoleReviewer:
		return NewToolSet(
			newReadFileTool(env),
		)
	case RoleAnalyst:
		return NewToolSet(
			newReadFileTool(env),
			newRunCommandTool(env, true),
		)
	default:
		return NewToolSet(newReadFileTool(env))
	}
}

func newReadFileTool(env ToolEnv) Tool {
	return Tool{
		Name:        "read_file",
		Description: "Read UTF-8 text from a file under the current workspace.",
		Execute: func(ctx context.Context, params map[string]any) (ToolResult, error) {
			if err := checkContextCancelled(ctx); err != nil {
				return ToolResult{}, err
			}
			path, err := requiredStringParam(params, "path")
			if err != nil {
				return ToolResult{}, err
			}
			root := effectiveWorkingDir(env.WorkingDir)
			absPath, relPath, err := resolveWorkspacePath(root, path)
			if err != nil {
				return ToolResult{}, err
			}
			emitToolEvent(env, EventReading, fmt.Sprintf("reading %s", relPath), map[string]any{"path": relPath})

			content, err := os.ReadFile(absPath)
			if err != nil {
				return ToolResult{}, err
			}
			return ToolResult{
				Output: string(content),
				Data: map[string]any{
					"path": relPath,
				},
			}, nil
		},
	}
}

func newWriteFileTool(env ToolEnv) Tool {
	return Tool{
		Name:        "write_file",
		Description: "Write full file content to a workspace path.",
		Execute: func(ctx context.Context, params map[string]any) (ToolResult, error) {
			if err := checkContextCancelled(ctx); err != nil {
				return ToolResult{}, err
			}
			path, err := requiredStringParam(params, "path")
			if err != nil {
				return ToolResult{}, err
			}
			content, err := requiredStringParam(params, "content")
			if err != nil {
				return ToolResult{}, err
			}
			root := effectiveWorkingDir(env.WorkingDir)
			absPath, relPath, err := resolveWorkspacePath(root, path)
			if err != nil {
				return ToolResult{}, err
			}

			var oldContent string
			if existing, readErr := os.ReadFile(absPath); readErr == nil {
				oldContent = string(existing)
			} else if !errors.Is(readErr, os.ErrNotExist) {
				return ToolResult{}, readErr
			}

			emitToolEvent(env, EventWriting, fmt.Sprintf("writing %s", relPath), map[string]any{"path": relPath})

			if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
				return ToolResult{}, err
			}
			if err := os.WriteFile(absPath, []byte(content), 0o644); err != nil {
				return ToolResult{}, err
			}
			if shouldEmitFileDiff(relPath) {
				emitToolEvent(env, EventFileDiff, fmt.Sprintf("diff %s", relPath), FileDiffPayload{
					Path:     relPath,
					OldLines: splitLinesForDiff(oldContent),
					NewLines: splitLinesForDiff(content),
				})
			}
			return ToolResult{
				Output: "ok",
				Data: map[string]any{
					"path": relPath,
				},
			}, nil
		},
	}
}

var planPathPattern = regexp.MustCompile(`^\.orchestra/plans/[A-Za-z0-9._-]+\.md$`)

func newWritePlanTool(env ToolEnv) Tool {
	return Tool{
		Name:        "write_plan_md",
		Description: "Write a task plan markdown file only to .orchestra/plans/<task_id>.md.",
		Execute: func(ctx context.Context, params map[string]any) (ToolResult, error) {
			if err := checkContextCancelled(ctx); err != nil {
				return ToolResult{}, err
			}
			path, err := requiredStringParam(params, "path")
			if err != nil {
				return ToolResult{}, err
			}
			content, err := requiredStringParam(params, "content")
			if err != nil {
				return ToolResult{}, err
			}

			normalized := filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
			normalized = strings.TrimPrefix(normalized, "./")
			if !planPathPattern.MatchString(normalized) {
				return ToolResult{}, fmt.Errorf("write_plan_md can only write .orchestra/plans/<task_id>.md")
			}

			root := effectiveWorkingDir(env.WorkingDir)
			absPath, relPath, err := resolveWorkspacePath(root, normalized)
			if err != nil {
				return ToolResult{}, err
			}

			emitToolEvent(env, EventWriting, fmt.Sprintf("writing %s", relPath), map[string]any{"path": relPath})

			if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
				return ToolResult{}, err
			}
			if err := os.WriteFile(absPath, []byte(content), 0o644); err != nil {
				return ToolResult{}, err
			}
			return ToolResult{
				Output: "ok",
				Data: map[string]any{
					"path": relPath,
				},
			}, nil
		},
	}
}

func newRunCommandTool(env ToolEnv, readOnly bool) Tool {
	description := "Run a shell command in the workspace."
	if readOnly {
		description = "Run a read-only shell command in the workspace (no writes)."
	}
	return Tool{
		Name:        "run_command",
		Description: description,
		Execute: func(ctx context.Context, params map[string]any) (ToolResult, error) {
			if err := checkContextCancelled(ctx); err != nil {
				return ToolResult{}, err
			}
			command, err := requiredStringParam(params, "command")
			if err != nil {
				return ToolResult{}, err
			}
			command = strings.TrimSpace(command)
			if command == "" {
				return ToolResult{}, fmt.Errorf("%w: command", errMissingParameter)
			}
			if readOnly && !isReadOnlyCommand(command) {
				return ToolResult{}, errors.New("read-only run_command rejected non read-only command")
			}

			emitToolEvent(env, EventRunning, fmt.Sprintf("running %s", command), map[string]any{"command": command})

			cmd := exec.CommandContext(ctx, "bash", "-lc", command)
			cmd.Dir = effectiveWorkingDir(env.WorkingDir)
			out, err := cmd.CombinedOutput()
			output := strings.TrimSpace(string(out))
			if err != nil {
				err = normalizeCancellationErr(err)
				if IsUserCancelled(err) {
					return ToolResult{}, err
				}
				if output == "" {
					return ToolResult{}, err
				}
				return ToolResult{}, fmt.Errorf("%w: %s", err, output)
			}
			return ToolResult{
				Output: output,
				Data: map[string]any{
					"command": command,
				},
			}, nil
		},
	}
}

func requiredStringParam(params map[string]any, key string) (string, error) {
	raw, ok := params[key]
	if !ok {
		return "", fmt.Errorf("%w: %s", errMissingParameter, key)
	}
	value, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("parameter %q must be a string", key)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%w: %s", errMissingParameter, key)
	}
	return value, nil
}

func effectiveWorkingDir(workingDir string) string {
	workingDir = strings.TrimSpace(workingDir)
	if workingDir == "" {
		return "."
	}
	return workingDir
}

func resolveWorkspacePath(root, path string) (absPath string, relPath string, err error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", "", err
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return "", "", errors.New("path is empty")
	}

	var targetAbs string
	if filepath.IsAbs(path) {
		targetAbs = filepath.Clean(path)
	} else {
		targetAbs = filepath.Clean(filepath.Join(rootAbs, path))
	}

	relToRoot, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil {
		return "", "", err
	}
	relToRoot = filepath.Clean(relToRoot)
	if relToRoot == ".." || strings.HasPrefix(relToRoot, ".."+string(filepath.Separator)) {
		return "", "", errors.New("path escapes workspace root")
	}
	return targetAbs, filepath.ToSlash(relToRoot), nil
}

func emitToolEvent(env ToolEnv, eventType AgentEventType, detail string, payload any) {
	if env.Emit == nil {
		return
	}
	env.Emit(AgentEvent{
		Type:    eventType,
		Role:    env.Role,
		Detail:  strings.TrimSpace(detail),
		Payload: payload,
	})
}

func splitLinesForDiff(content string) []string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	if content == "" {
		return nil
	}
	lines := strings.Split(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func shouldEmitFileDiff(relPath string) bool {
	normalized := filepath.ToSlash(filepath.Clean(strings.TrimSpace(relPath)))
	normalized = strings.TrimPrefix(normalized, "./")
	if normalized == ".orchestra" {
		return false
	}
	return !strings.HasPrefix(normalized, ".orchestra/")
}

func isReadOnlyCommand(command string) bool {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return false
	}
	disallowSnippets := []string{
		">", ">>", "|", ";", "&&", "||", "$(", "`",
	}
	for _, snippet := range disallowSnippets {
		if strings.Contains(trimmed, snippet) {
			return false
		}
	}

	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return false
	}
	switch fields[0] {
	case "ls", "pwd", "cat", "head", "tail", "wc", "find", "rg", "grep", "awk":
		return true
	case "sed":
		for _, f := range fields[1:] {
			if strings.HasPrefix(f, "-i") {
				return false
			}
		}
		return true
	case "git":
		if len(fields) < 2 {
			return false
		}
		switch fields[1] {
		case "status", "log", "diff", "show", "grep", "branch", "remote", "rev-parse", "ls-files":
			return true
		default:
			return false
		}
	default:
		return false
	}
}
