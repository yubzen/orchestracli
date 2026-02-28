package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/yubzen/orchestra/internal/mcp"
	"github.com/yubzen/orchestra/internal/providers"
	"github.com/yubzen/orchestra/internal/rag"
	"github.com/yubzen/orchestra/internal/state"
)

type Role string

const (
	RolePlanner  Role = "planner"
	RoleCoder    Role = "coder"
	RoleReviewer Role = "reviewer"
	RoleAnalyst  Role = "analyst"
)

type Agent struct {
	Role             Role
	Model            string
	Provider         providers.Provider
	SystemPrompt     string
	TaskSystemPrompt string
	ChatSystemPrompt string
	AllowedTools     []string
	ToolSet          ToolSet
	RAG              *rag.Store
	Indexer          *rag.Indexer
}

type RunOptions struct {
	Mode       DispatchMode
	OnToken    func(token string)
	OnToolCall func(name string, params map[string]any, result ToolResult, err error)
}

var ErrAgentNotReady = errors.New("agent is not initialized")

func NewAgent(role Role, model string, provider providers.Provider, store *rag.Store, indexer *rag.Indexer) *Agent {
	toolSet := DefaultToolSetForRole(role, ToolEnv{
		WorkingDir: ".",
		Role:       role,
	})
	basePrompt := strings.TrimSpace(LoadSystemPrompt(string(role)))
	taskPrompt := buildTaskSystemPrompt(role, basePrompt)
	chatPrompt := buildChatSystemPrompt(role)
	return &Agent{
		Role:             role,
		Model:            model,
		Provider:         provider,
		SystemPrompt:     chatPrompt,
		TaskSystemPrompt: taskPrompt,
		ChatSystemPrompt: chatPrompt,
		AllowedTools:     toolSet.Names(),
		ToolSet:          toolSet,
		RAG:              store,
		Indexer:          indexer,
	}
}

func (a *Agent) Validate() error {
	if a == nil {
		return ErrAgentNotReady
	}
	if a.Provider == nil {
		return fmt.Errorf("%s agent provider is not configured", a.Role)
	}
	if strings.TrimSpace(a.Model) == "" {
		return fmt.Errorf("%s agent model is empty", a.Role)
	}
	if strings.TrimSpace(a.TaskSystemPrompt) == "" {
		return fmt.Errorf("%s agent system prompt is empty", a.Role)
	}
	if strings.TrimSpace(a.ChatSystemPrompt) == "" {
		return fmt.Errorf("%s agent chat prompt is empty", a.Role)
	}
	if strings.TrimSpace(a.SystemPrompt) == "" {
		a.SystemPrompt = a.ChatSystemPrompt
	}
	if len(a.ToolSet.Names()) == 0 {
		a.BindToolSet(ToolEnv{
			WorkingDir: ".",
			Role:       a.Role,
		})
	}
	if len(a.ToolSet.Names()) == 0 {
		return fmt.Errorf("%s agent tools are not configured", a.Role)
	}
	return nil
}

func (a *Agent) BindToolSet(env ToolEnv) {
	if a == nil {
		return
	}
	env.Role = a.Role
	a.ToolSet = DefaultToolSetForRole(a.Role, env)
	a.AllowedTools = a.ToolSet.Names()
}

func (a *Agent) SetSystemPrompt(prompt string) {
	if a == nil {
		return
	}
	a.SystemPrompt = strings.TrimSpace(prompt)
}

func (a *Agent) SetDispatchMode(mode DispatchMode) {
	if a == nil {
		return
	}
	mode = mode.Normalize()
	switch mode {
	case DispatchModeTask:
		a.SetSystemPrompt(a.TaskSystemPrompt)
	default:
		a.SetSystemPrompt(a.ChatSystemPrompt)
	}
}

func (a *Agent) ExecuteTool(ctx context.Context, name string, params map[string]any) (ToolResult, error) {
	if a == nil {
		return ToolResult{}, ErrAgentNotReady
	}
	tool, ok := a.ToolSet.Get(name)
	if !ok {
		return ToolResult{}, fmt.Errorf("%w: %s", errToolNotAllowed, strings.TrimSpace(name))
	}
	if tool.Execute == nil {
		return ToolResult{}, fmt.Errorf("tool %q has no executor", tool.Name)
	}
	return tool.Execute(ctx, params)
}

func (a *Agent) Run(ctx context.Context, userPrompt string, session *state.Session, db *state.DB) (string, error) {
	mode := dispatchModeForInput(userPrompt)
	return a.RunWithOptions(ctx, userPrompt, session, db, RunOptions{
		Mode: mode,
	})
}

func (a *Agent) RunWithOptions(ctx context.Context, userPrompt string, session *state.Session, db *state.DB, options RunOptions) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := checkContextCancelled(ctx); err != nil {
		return "", err
	}

	if err := a.Validate(); err != nil {
		return "", err
	}
	if err := a.Provider.Ping(ctx); err != nil {
		err = normalizeCancellationErr(err)
		if IsUserCancelled(err) {
			return "", err
		}
		return "", fmt.Errorf("%s agent provider is not ready: %w", a.Role, err)
	}
	dispatchMode := options.Mode
	if strings.TrimSpace(string(dispatchMode)) == "" {
		dispatchMode = dispatchModeForInput(userPrompt)
	} else {
		dispatchMode = dispatchMode.Normalize()
	}
	a.SetDispatchMode(dispatchMode)

	var ragContext string
	if dispatchMode == DispatchModeTask && a.Indexer != nil {
		chunks, err := a.Indexer.Query(ctx, userPrompt)
		if err != nil {
			err = normalizeCancellationErr(err)
			if IsUserCancelled(err) {
				return "", err
			}
		}
		if err == nil && len(chunks) > 0 {
			var sb strings.Builder
			sb.WriteString("Relevant codebase context:\n")
			for _, chunk := range chunks {
				sb.WriteString(fmt.Sprintf("---\nFile: %s\n%s\n", chunk.Filepath, mcp.Clean(chunk.Content)))
			}
			ragContext = sb.String()
		}
	}

	systemPrompt := strings.TrimSpace(a.SystemPrompt)
	if promptBlock := strings.TrimSpace(a.ToolSet.PromptBlock()); promptBlock != "" {
		systemPrompt = strings.TrimSpace(systemPrompt + "\n\n" + promptBlock)
	}

	messages := []providers.Message{
		{Role: "system", Content: systemPrompt},
	}

	if db != nil && session != nil {
		history, err := db.GetMessages(ctx, session.ID)
		if err != nil {
			err = normalizeCancellationErr(err)
			if IsUserCancelled(err) {
				return "", err
			}
		} else {
			for _, h := range history {
				messages = append(messages, providers.Message{
					Role:    h.Role,
					Content: h.Content,
				})
			}
		}
	}

	var finalPrompt string
	if ragContext != "" {
		finalPrompt = ragContext + "\n\nUser Prompt:\n" + userPrompt
	} else {
		finalPrompt = userPrompt
	}

	finalPrompt = mcp.Clean(finalPrompt)
	messages = append(messages, providers.Message{
		Role:    "user",
		Content: finalPrompt,
	})

	for i, m := range messages {
		messages[i].Content = mcp.Clean(m.Content)
	}

	// Serialize agent tools into provider-compatible format.
	pTools := a.ToolSet.ProviderTools()

	// Agentic tool dispatch loop: send to LLM, execute tool calls, feed
	// results back, repeat until the LLM returns a final text response.
	const maxIterations = 25
	var finalText string

	for iteration := 0; iteration < maxIterations; iteration++ {
		if err := checkContextCancelled(ctx); err != nil {
			return "", err
		}
		response, err := a.Provider.Complete(ctx, a.Model, messages, pTools, options.OnToken)
		if err != nil {
			err = normalizeCancellationErr(err)
			if IsUserCancelled(err) {
				return "", err
			}
			return "", err
		}

		if !response.HasToolCalls() {
			finalText = mcp.Clean(response.Text)
			break
		}

		// Append the assistant message with tool calls to the conversation.
		messages = append(messages, providers.Message{
			Role:      "assistant",
			Content:   response.Text,
			ToolCalls: response.ToolCalls,
		})

		// Execute each tool call and append results.
		for _, tc := range response.ToolCalls {
			if err := checkContextCancelled(ctx); err != nil {
				return "", err
			}
			params, parseErr := parseToolArguments(tc.Arguments)
			var resultContent string
			if parseErr != nil {
				if options.OnToolCall != nil {
					options.OnToolCall(tc.Name, nil, ToolResult{}, parseErr)
				}
				resultContent = fmt.Sprintf("error: invalid tool arguments for %s: %v", strings.TrimSpace(tc.Name), parseErr)
			} else {
				result, execErr := a.ExecuteTool(ctx, tc.Name, params)
				if options.OnToolCall != nil {
					options.OnToolCall(tc.Name, params, result, execErr)
				}
				if execErr != nil {
					execErr = normalizeCancellationErr(execErr)
					if IsUserCancelled(execErr) {
						return "", execErr
					}
					resultContent = fmt.Sprintf(`{"ok":false,"error":%q}`, execErr.Error())
				} else {
					payload := map[string]any{
						"ok":     true,
						"output": result.Output,
					}
					if len(result.Data) > 0 {
						payload["data"] = result.Data
					}
					raw, err := json.Marshal(payload)
					if err != nil {
						resultContent = result.Output
					} else {
						resultContent = string(raw)
					}
				}
			}

			messages = append(messages, providers.Message{
				Role:       "tool",
				Content:    resultContent,
				ToolCallID: tc.ID,
			})
		}
		// Continue the loop — the LLM will see the tool results and either
		// call more tools or produce a final text response.
	}
	if strings.TrimSpace(finalText) == "" {
		return "", errors.New("agent exceeded tool-call iteration limit without a final response")
	}

	if db != nil && session != nil {
		_ = db.SaveMessage(ctx, session.ID, "user", string(a.Role), finalPrompt, 0)
		_ = db.SaveMessage(ctx, session.ID, "assistant", string(a.Role), finalText, 0)
	}

	return finalText, nil
}

func parseToolArguments(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}

	var params map[string]any
	if err := json.Unmarshal(raw, &params); err == nil {
		return params, nil
	}

	// Some providers return arguments as an encoded JSON string.
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err == nil {
		encoded = strings.TrimSpace(encoded)
		if encoded == "" {
			return map[string]any{}, nil
		}
		if err := json.Unmarshal([]byte(encoded), &params); err == nil {
			return params, nil
		}
	}

	return nil, fmt.Errorf("unable to parse tool arguments: %s", string(raw))
}
