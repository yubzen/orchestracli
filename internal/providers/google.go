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

	"github.com/zalando/go-keyring"
)

type Google struct {
	Client *http.Client
}

func NewGoogle() *Google {
	return &Google{Client: &http.Client{}}
}

func (p *Google) Name() string {
	return "google"
}

func (p *Google) getKey() (string, error) {
	key, err := keyring.Get("orchestra", "google")
	if err != nil || key == "" {
		return "", &ProviderAuthError{ProviderName: "google", Msg: "API key not found in keyring"}
	}
	return key, nil
}

func (p *Google) Ping(ctx context.Context) error {
	_, err := p.getKey()
	return err // Basic ping
}

func (p *Google) ListModels(ctx context.Context) ([]string, error) {
	key, err := p.getKey()
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models?key=%s", key)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := p.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden ||
			(resp.StatusCode == http.StatusBadRequest && bytes.Contains(body, []byte("API key not valid"))) {
			return nil, &ProviderAuthError{ProviderName: "google", Msg: "Unauthorized: Invalid API key"}
		}
		return nil, fmt.Errorf("failed to list models, status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var result struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	models := make([]string, 0, len(result.Models))
	for _, model := range result.Models {
		name := strings.TrimSpace(model.Name)
		name = strings.TrimPrefix(name, "models/")
		if name == "" {
			continue
		}
		models = append(models, name)
	}
	return models, nil
}

func (p *Google) Complete(ctx context.Context, model string, messages []Message, tools []Tool, onToken TokenCallback) (CompletionResponse, error) {
	key, err := p.getKey()
	if err != nil {
		return CompletionResponse{}, err
	}

	var reqContents []map[string]interface{}
	var systemInstructions map[string]interface{}

	for _, m := range messages {
		if m.Role == "system" {
			systemInstructions = map[string]interface{}{
				"parts": []map[string]string{{"text": m.Content}},
			}
			continue
		}
		if m.Role == "tool" {
			// Tool result as a function response in Google format.
			var resultData interface{}
			_ = json.Unmarshal([]byte(m.Content), &resultData)
			if resultData == nil {
				resultData = map[string]interface{}{"result": m.Content}
			}
			reqContents = append(reqContents, map[string]interface{}{
				"role": "function",
				"parts": []map[string]interface{}{
					{
						"functionResponse": map[string]interface{}{
							"name":     m.ToolCallID,
							"response": resultData,
						},
					},
				},
			})
			continue
		}
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			var parts []map[string]interface{}
			if strings.TrimSpace(m.Content) != "" {
				parts = append(parts, map[string]interface{}{"text": m.Content})
			}
			for _, tc := range m.ToolCalls {
				var args interface{}
				if len(tc.Arguments) > 0 {
					_ = json.Unmarshal(tc.Arguments, &args)
				}
				parts = append(parts, map[string]interface{}{
					"functionCall": map[string]interface{}{
						"name": tc.Name,
						"args": args,
					},
				})
			}
			reqContents = append(reqContents, map[string]interface{}{
				"role":  "model",
				"parts": parts,
			})
			continue
		}

		role := "user"
		if m.Role == "assistant" {
			role = "model"
		}
		reqContents = append(reqContents, map[string]interface{}{
			"role":  role,
			"parts": []map[string]string{{"text": m.Content}},
		})
	}

	payload := map[string]interface{}{
		"contents": reqContents,
	}
	if systemInstructions != nil {
		payload["system_instruction"] = systemInstructions
	}

	hasTools := len(tools) > 0
	if hasTools {
		var fgTools []map[string]interface{}
		for _, t := range tools {
			toolDef := map[string]interface{}{
				"name":        t.Name,
				"description": t.Description,
			}
			if t.InputSchema != nil {
				toolDef["parameters"] = t.InputSchema
			}
			fgTools = append(fgTools, toolDef)
		}
		payload["tools"] = []map[string]interface{}{
			{"function_declarations": fgTools},
		}
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return CompletionResponse{}, err
	}

	// Use non-streaming for tool calls, streaming for text-only.
	var apiURL string
	if hasTools {
		apiURL = fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", model, key)
	} else {
		apiURL = fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:streamGenerateContent?alt=sse&key=%s", model, key)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return CompletionResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if !hasTools {
		req.Header.Set("Accept", "text/event-stream")
	}

	resp, err := p.Client.Do(req)
	if err != nil {
		return CompletionResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusUnauthorized || (resp.StatusCode == http.StatusBadRequest && bytes.Contains(body, []byte("API key not valid"))) {
			return CompletionResponse{}, &ProviderAuthError{ProviderName: "google", Msg: "Unauthorized: Invalid API key"}
		}
		return CompletionResponse{}, fmt.Errorf("google error: %s (status %d)", string(body), resp.StatusCode)
	}

	// Streaming text-only path.
	contentType := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
	if !hasTools && strings.Contains(contentType, "text/event-stream") {
		text, err := readGoogleStream(resp.Body, onToken)
		if err != nil {
			return CompletionResponse{}, err
		}
		return CompletionResponse{Text: text, StopReason: "stop"}, nil
	}

	// Non-streaming path — parse full response including function calls.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return CompletionResponse{}, err
	}
	return decodeGoogleFullResponse(body, onToken)
}

func readGoogleStream(body io.Reader, onToken TokenCallback) (string, error) {
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
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
				} `json:"content"`
			} `json:"candidates"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		for _, candidate := range chunk.Candidates {
			for _, part := range candidate.Content.Parts {
				token := part.Text
				if token == "" {
					continue
				}
				out.WriteString(token)
				if onToken != nil {
					onToken(token)
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	result := strings.TrimSpace(out.String())
	if result == "" {
		return "", fmt.Errorf("empty response from google")
	}
	return result, nil
}

// decodeGoogleFullResponse parses a non-streaming Google response extracting
// text and functionCall parts.
func decodeGoogleFullResponse(body []byte, onToken TokenCallback) (CompletionResponse, error) {
	var result struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text         string `json:"text"`
					FunctionCall *struct {
						Name string          `json:"name"`
						Args json.RawMessage `json:"args"`
					} `json:"functionCall"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return CompletionResponse{}, fmt.Errorf("google decode error: %w", err)
	}
	if len(result.Candidates) == 0 {
		return CompletionResponse{}, fmt.Errorf("empty response from google")
	}

	candidate := result.Candidates[0]
	resp := CompletionResponse{StopReason: candidate.FinishReason}
	var textParts []string

	for i, part := range candidate.Content.Parts {
		if part.FunctionCall != nil {
			resp.ToolCalls = append(resp.ToolCalls, ToolCall{
				ID:        fmt.Sprintf("google-call-%d", i),
				Name:      part.FunctionCall.Name,
				Arguments: part.FunctionCall.Args,
			})
		}
		text := strings.TrimSpace(part.Text)
		if text != "" {
			textParts = append(textParts, text)
			if onToken != nil {
				onToken(text)
			}
		}
	}

	resp.Text = strings.Join(textParts, "\n")
	if len(resp.ToolCalls) == 0 && resp.Text == "" {
		return CompletionResponse{}, fmt.Errorf("empty response from google")
	}
	return resp, nil
}
