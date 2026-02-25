package agent

import (
	"context"
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
	Role         Role
	Model        string
	Provider     providers.Provider
	SystemPrompt string
	AllowedTools []string
	RAG          *rag.Store
	Indexer      *rag.Indexer
}

var ErrAgentNotReady = errors.New("agent is not initialized")

func NewAgent(role Role, model string, provider providers.Provider, store *rag.Store, indexer *rag.Indexer) *Agent {
	return &Agent{
		Role:         role,
		Model:        model,
		Provider:     provider,
		SystemPrompt: LoadSystemPrompt(string(role)),
		AllowedTools: LoadAllowedTools(string(role)),
		RAG:          store,
		Indexer:      indexer,
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
	if strings.TrimSpace(a.SystemPrompt) == "" {
		return fmt.Errorf("%s agent system prompt is empty", a.Role)
	}
	return nil
}

func (a *Agent) Run(ctx context.Context, userPrompt string, session *state.Session, db *state.DB) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	if err := a.Validate(); err != nil {
		return "", err
	}
	if err := a.Provider.Ping(ctx); err != nil {
		return "", fmt.Errorf("%s agent provider is not ready: %w", a.Role, err)
	}

	var ragContext string
	if a.Indexer != nil {
		chunks, err := a.Indexer.Query(ctx, userPrompt)
		if err == nil && len(chunks) > 0 {
			var sb strings.Builder
			sb.WriteString("Relevant codebase context:\n")
			for _, chunk := range chunks {
				sb.WriteString(fmt.Sprintf("---\nFile: %s\n%s\n", chunk.Filepath, mcp.Clean(chunk.Content)))
			}
			ragContext = sb.String()
		}
	}

	messages := []providers.Message{
		{Role: "system", Content: a.SystemPrompt},
	}

	if db != nil && session != nil {
		history, _ := db.GetMessages(ctx, session.ID)
		for _, h := range history {
			messages = append(messages, providers.Message{
				Role:    h.Role,
				Content: h.Content,
			})
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

	var pTools []providers.Tool

	reply, err := a.Provider.Complete(ctx, a.Model, messages, pTools)
	if err != nil {
		return "", err
	}

	reply = mcp.Clean(reply)

	if db != nil && session != nil {
		_ = db.SaveMessage(ctx, session.ID, "user", string(a.Role), finalPrompt, 0)
		_ = db.SaveMessage(ctx, session.ID, "assistant", string(a.Role), reply, 0)
	}

	return reply, nil
}
