package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/zalando/go-keyring"
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
	key, err := keyring.Get("orchestra", "anthropic")
	if err != nil || key == "" {
		return "", &ProviderAuthError{ProviderName: "anthropic", Msg: "API key not found in keyring"}
	}
	return key, nil
}

func (p *Anthropic) Ping(ctx context.Context) error {
	_, err := p.getKey()
	return err // Basic ping by checking key presence
}

func (p *Anthropic) ListModels(ctx context.Context) ([]string, error) {
	return []string{"claude-3-5-sonnet-20241022", "claude-3-opus-20240229", "claude-3-haiku-20240307"}, nil
}

func (p *Anthropic) Complete(ctx context.Context, model string, messages []Message, tools []Tool) (string, error) {
	key, err := p.getKey()
	if err != nil {
		return "", err
	}

	var systemStr string
	var reqMessages []map[string]interface{}
	for _, m := range messages {
		if m.Role == "system" {
			systemStr += m.Content + "\n"
		} else {
			reqMessages = append(reqMessages, map[string]interface{}{
				"role":    m.Role,
				"content": m.Content,
			})
		}
	}

	payload := map[string]interface{}{
		"model":      model,
		"max_tokens": 8192,
		"messages":   reqMessages,
	}
	if systemStr != "" {
		payload["system"] = systemStr
	}

	// Basic tools support (if tools are provided)
	if len(tools) > 0 {
		payload["tools"] = tools
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewBuffer(bodyBytes))
	if err != nil {
		return "", err
	}

	req.Header.Set("x-api-key", key)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := p.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusUnauthorized {
			return "", &ProviderAuthError{ProviderName: "anthropic", Msg: "Unauthorized: Invalid API key"}
		}
		return "", fmt.Errorf("anthropic error: %s (status %d)", string(body), resp.StatusCode)
	}

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if len(result.Content) > 0 {
		return result.Content[0].Text, nil
	}
	return "", fmt.Errorf("empty response from anthropic")
}
