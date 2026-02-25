package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/zalando/go-keyring"
)

type OpenAI struct {
	BaseURL string
	KeyName string // e.g., "openai" or "deepseek"
	Client  *http.Client
}

func NewOpenAI(baseURL, keyName string) *OpenAI {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	if keyName == "" {
		keyName = "openai"
	}
	return &OpenAI{
		BaseURL: strings.TrimRight(baseURL, "/"),
		KeyName: keyName,
		Client:  &http.Client{},
	}
}

func (p *OpenAI) Name() string {
	return p.KeyName
}

func (p *OpenAI) getKey() (string, error) {
	key, err := keyring.Get("orchestra", p.KeyName)
	if err != nil || key == "" {
		return "", &ProviderAuthError{ProviderName: p.KeyName, Msg: "API key not found in keyring"}
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

	resp, err := p.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to list models, status %d", resp.StatusCode)
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
	if len(models) == 0 {
		models = []string{"gpt-4o", "gpt-4-turbo"} // Fallback
	}
	return models, nil
}

func (p *OpenAI) Complete(ctx context.Context, model string, messages []Message, tools []Tool) (string, error) {
	key, err := p.getKey()
	if err != nil {
		return "", err
	}

	var reqMessages []map[string]string
	for _, m := range messages {
		reqMessages = append(reqMessages, map[string]string{
			"role":    m.Role,
			"content": m.Content,
		})
	}

	payload := map[string]interface{}{
		"model":    model,
		"messages": reqMessages,
	}

	if len(tools) > 0 {
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
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", p.BaseURL+"/chat/completions", bytes.NewBuffer(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusUnauthorized {
			return "", &ProviderAuthError{ProviderName: p.KeyName, Msg: "Unauthorized: Invalid API key"}
		}
		return "", fmt.Errorf("openai compat error: %s (status %d)", string(body), resp.StatusCode)
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if len(result.Choices) > 0 {
		return result.Choices[0].Message.Content, nil
	}
	return "", fmt.Errorf("empty response")
}
