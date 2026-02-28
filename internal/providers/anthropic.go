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

type Anthropic struct {
	Client *http.Client
}

func NewAnthropic() *Anthropic {
	return &Anthropic{Client: &http.Client{}}
}

func (p *Anthropic) Name() string {
	return "anthropic"
}

func (p *Anthropic) getKey() (string, error) {
	key, err := LoadCredential("anthropic")
	if err != nil || strings.TrimSpace(key) == "" {
		return "", &ProviderAuthError{ProviderName: "anthropic", Msg: "API key not found. Run /connect to reconnect provider."}
	}
	return key, nil
}

func (p *Anthropic) Ping(ctx context.Context) error {
	_, err := p.getKey()
	return err // Basic ping by checking key presence
}

func (p *Anthropic) ListModels(ctx context.Context) ([]string, error) {
	key, err := p.getKey()
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.anthropic.com/v1/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", key)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := p.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return nil, &ProviderAuthError{ProviderName: "anthropic", Msg: "Unauthorized: Invalid API key"}
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
	models := make([]string, 0, len(result.Data))
	for _, item := range result.Data {
		if strings.TrimSpace(item.ID) == "" {
			continue
		}
		models = append(models, item.ID)
	}
	return models, nil
}

func (p *Anthropic) Complete(ctx context.Context, model string, messages []Message, tools []Tool, onToken TokenCallback) (CompletionResponse, error) {
	key, err := p.getKey()
	if err != nil {
		return CompletionResponse{}, err
	}

	var systemStr string
	var reqMessages []map[string]interface{}
	for _, m := range messages {
		if m.Role == "system" {
			systemStr += m.Content + "\n"
			continue
		}
		if m.Role == "tool" {
			// Tool result message for Anthropic format.
			reqMessages = append(reqMessages, map[string]interface{}{
				"role": "user",
				"content": []map[string]interface{}{
					{
						"type":        "tool_result",
						"tool_use_id": m.ToolCallID,
						"content":     m.Content,
					},
				},
			})
			continue
		}
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			// Reconstruct the assistant message with tool_use blocks.
			var contentBlocks []map[string]interface{}
			if strings.TrimSpace(m.Content) != "" {
				contentBlocks = append(contentBlocks, map[string]interface{}{
					"type": "text",
					"text": m.Content,
				})
			}
			for _, tc := range m.ToolCalls {
				var args interface{}
				if len(tc.Arguments) > 0 {
					_ = json.Unmarshal(tc.Arguments, &args)
				}
				contentBlocks = append(contentBlocks, map[string]interface{}{
					"type":  "tool_use",
					"id":    tc.ID,
					"name":  tc.Name,
					"input": args,
				})
			}
			reqMessages = append(reqMessages, map[string]interface{}{
				"role":    "assistant",
				"content": contentBlocks,
			})
			continue
		}
		reqMessages = append(reqMessages, map[string]interface{}{
			"role":    m.Role,
			"content": m.Content,
		})
	}

	payload := map[string]interface{}{
		"model":      model,
		"max_tokens": 8192,
		"messages":   reqMessages,
	}
	if systemStr != "" {
		payload["system"] = strings.TrimSpace(systemStr)
	}

	hasTools := len(tools) > 0
	if hasTools {
		var anthropicTools []map[string]interface{}
		for _, t := range tools {
			anthropicTools = append(anthropicTools, map[string]interface{}{
				"name":         t.Name,
				"description":  t.Description,
				"input_schema": t.InputSchema,
			})
		}
		payload["tools"] = anthropicTools
		// Use non-streaming for tool calls to simplify tool_use block parsing.
		payload["stream"] = false
	} else {
		payload["stream"] = true
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return CompletionResponse{}, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewBuffer(bodyBytes))
	if err != nil {
		return CompletionResponse{}, err
	}

	req.Header.Set("x-api-key", key)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")
	if !hasTools {
		req.Header.Set("accept", "text/event-stream")
	}

	resp, err := p.Client.Do(req)
	if err != nil {
		return CompletionResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusUnauthorized {
			return CompletionResponse{}, &ProviderAuthError{ProviderName: "anthropic", Msg: "Unauthorized: Invalid API key"}
		}
		return CompletionResponse{}, fmt.Errorf("anthropic error: %s (status %d)", string(body), resp.StatusCode)
	}

	// Streaming text-only path.
	contentType := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
	if !hasTools && strings.Contains(contentType, "text/event-stream") {
		text, err := readAnthropicStream(resp.Body, onToken)
		if err != nil {
			return CompletionResponse{}, err
		}
		return CompletionResponse{Text: text, StopReason: "end_turn"}, nil
	}

	// Non-streaming path — parse full response including tool_use blocks.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return CompletionResponse{}, err
	}
	return decodeAnthropicResponse(body, onToken)
}

// decodeAnthropicResponse parses a non-streaming Anthropic response extracting
// both text and tool_use content blocks.
func decodeAnthropicResponse(body []byte, onToken TokenCallback) (CompletionResponse, error) {
	var result struct {
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return CompletionResponse{}, fmt.Errorf("anthropic decode error: %w", err)
	}

	resp := CompletionResponse{StopReason: result.StopReason}
	var textParts []string

	for _, block := range result.Content {
		switch block.Type {
		case "text":
			text := strings.TrimSpace(block.Text)
			if text != "" {
				textParts = append(textParts, text)
				if onToken != nil {
					onToken(text)
				}
			}
		case "tool_use":
			resp.ToolCalls = append(resp.ToolCalls, ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: block.Input,
			})
		}
	}

	resp.Text = strings.Join(textParts, "\n")
	if len(resp.ToolCalls) == 0 && resp.Text == "" {
		return CompletionResponse{}, fmt.Errorf("empty response from anthropic")
	}
	return resp, nil
}

func readAnthropicStream(body io.Reader, onToken TokenCallback) (string, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	var out strings.Builder
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}

		var chunk struct {
			Type  string `json:"type"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
			ContentBlock struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content_block"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}

		token := chunk.Delta.Text
		if token == "" {
			token = chunk.ContentBlock.Text
		}
		if token == "" {
			continue
		}
		out.WriteString(token)
		if onToken != nil {
			onToken(token)
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	result := strings.TrimSpace(out.String())
	if result == "" {
		return "", fmt.Errorf("empty response from anthropic")
	}
	return result, nil
}
