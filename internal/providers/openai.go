package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type OpenAI struct {
	BaseURL      string
	KeyName      string // e.g., "openai" or "deepseek"
	Client       *http.Client
	ExtraHeaders map[string]string
}

func NewOpenAI(baseURL, keyName string) *OpenAI {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	if keyName == "" {
		keyName = "openai"
	}
	return &OpenAI{
		BaseURL:      strings.TrimRight(baseURL, "/"),
		KeyName:      keyName,
		Client:       &http.Client{},
		ExtraHeaders: nil,
	}
}

func (p *OpenAI) Name() string {
	return p.KeyName
}

func (p *OpenAI) getKey() (string, error) {
	key, err := LoadCredential(p.KeyName)
	if err != nil || strings.TrimSpace(key) == "" {
		return "", &ProviderAuthError{ProviderName: p.KeyName, Msg: "API key not found. Run /connect to reconnect provider."}
	}
	return key, nil
}

func (p *OpenAI) Ping(ctx context.Context) error {
	_, err := p.getKey()
	return err // Basic ping
}

func (p *OpenAI) ListModels(ctx context.Context) ([]string, error) {
	key, err := p.getKey()
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "GET", p.BaseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	for k, v := range p.ExtraHeaders {
		if strings.TrimSpace(k) == "" || strings.TrimSpace(v) == "" {
			continue
		}
		req.Header.Set(k, v)
	}

	resp, err := p.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return nil, &ProviderAuthError{ProviderName: p.KeyName, Msg: "Unauthorized: Invalid API key"}
		}
		return nil, fmt.Errorf("failed to list models, status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	var models []string
	for _, m := range result.Data {
		models = append(models, m.ID)
	}
	return models, nil
}

func (p *OpenAI) Complete(ctx context.Context, model string, messages []Message, tools []Tool, onToken TokenCallback) (CompletionResponse, error) {
	key, err := p.getKey()
	if err != nil {
		return CompletionResponse{}, err
	}

	var reqMessages []map[string]interface{}
	for _, m := range messages {
		if m.Role == "tool" {
			reqMessages = append(reqMessages, map[string]interface{}{
				"role":         "tool",
				"content":      m.Content,
				"tool_call_id": m.ToolCallID,
			})
			continue
		}
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			var openaiToolCalls []map[string]interface{}
			for _, tc := range m.ToolCalls {
				openaiToolCalls = append(openaiToolCalls, map[string]interface{}{
					"id":   tc.ID,
					"type": "function",
					"function": map[string]interface{}{
						"name":      tc.Name,
						"arguments": string(tc.Arguments),
					},
				})
			}
			msg := map[string]interface{}{
				"role":       "assistant",
				"tool_calls": openaiToolCalls,
			}
			if strings.TrimSpace(m.Content) != "" {
				msg["content"] = m.Content
			}
			reqMessages = append(reqMessages, msg)
			continue
		}
		reqMessages = append(reqMessages, map[string]interface{}{
			"role":    m.Role,
			"content": m.Content,
		})
	}

	hasTools := len(tools) > 0
	payload := map[string]interface{}{
		"model":    model,
		"messages": reqMessages,
	}

	if hasTools {
		var openaiTools []map[string]interface{}
		for _, t := range tools {
			openaiTools = append(openaiTools, map[string]interface{}{
				"type": "function",
				"function": map[string]interface{}{
					"name":        t.Name,
					"description": t.Description,
					"parameters":  t.InputSchema,
				},
			})
		}
		payload["tools"] = openaiTools
		// Non-streaming for tool calls to simplify parsing.
		payload["stream"] = false
	} else {
		payload["stream"] = true
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return CompletionResponse{}, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", p.BaseURL+"/chat/completions", bytes.NewBuffer(bodyBytes))
	if err != nil {
		return CompletionResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range p.ExtraHeaders {
		if strings.TrimSpace(k) == "" || strings.TrimSpace(v) == "" {
			continue
		}
		req.Header.Set(k, v)
	}

	resp, err := p.Client.Do(req)
	if err != nil {
		return CompletionResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusUnauthorized {
			return CompletionResponse{}, &ProviderAuthError{ProviderName: p.KeyName, Msg: "Unauthorized: Invalid API key"}
		}
		return CompletionResponse{}, fmt.Errorf("openai compat error: %s (status %d)", string(body), resp.StatusCode)
	}

	// Streaming text-only path.
	contentType := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
	if !hasTools && strings.Contains(contentType, "text/event-stream") {
		text, err := readOpenAIStream(resp.Body, onToken)
		if err != nil {
			return CompletionResponse{}, err
		}
		return CompletionResponse{Text: text, StopReason: "stop"}, nil
	}

	// Non-streaming path — parse full response including tool calls.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return CompletionResponse{}, err
	}
	return decodeOpenAIFullResponse(body, onToken)
}

func readOpenAIStream(body io.Reader, onToken TokenCallback) (string, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	var out strings.Builder
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			break
		}

		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		for _, choice := range chunk.Choices {
			token := choice.Delta.Content
			if token == "" {
				token = choice.Message.Content
			}
			if token == "" {
				continue
			}
			out.WriteString(token)
			if onToken != nil {
				onToken(token)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	result := strings.TrimSpace(out.String())
	if result == "" {
		return "", fmt.Errorf("empty response")
	}
	return result, nil
}

// decodeOpenAIFullResponse parses a non-streaming OpenAI response, extracting
// both message content and tool call blocks.
func decodeOpenAIFullResponse(body []byte, onToken TokenCallback) (CompletionResponse, error) {
	var result struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string          `json:"name"`
						Arguments json.RawMessage `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return CompletionResponse{}, err
	}
	if len(result.Choices) == 0 {
		return CompletionResponse{}, fmt.Errorf("empty response")
	}

	choice := result.Choices[0]
	resp := CompletionResponse{
		Text:       strings.TrimSpace(choice.Message.Content),
		StopReason: choice.FinishReason,
	}

	for _, tc := range choice.Message.ToolCalls {
		arguments := tc.Function.Arguments
		if len(arguments) == 0 {
			arguments = json.RawMessage(`{}`)
		}
		// OpenAI-compatible providers are inconsistent here:
		// - standard OpenAI: arguments is a JSON string
		// - some compatibles: arguments is already a JSON object
		// Normalize both to raw object JSON for downstream tool parsing.
		if len(arguments) > 0 && arguments[0] == '"' {
			var encoded string
			if err := json.Unmarshal(arguments, &encoded); err == nil {
				arguments = json.RawMessage(encoded)
			}
		}
		resp.ToolCalls = append(resp.ToolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: arguments,
		})
	}

	if resp.Text != "" && onToken != nil {
		onToken(resp.Text)
	}

	if len(resp.ToolCalls) == 0 && resp.Text == "" {
		return CompletionResponse{}, fmt.Errorf("empty response")
	}
	return resp, nil
}
