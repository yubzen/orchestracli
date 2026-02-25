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
	return []string{"gemini-2.5-pro", "gemini-2.5-flash", "gemini-1.5-pro"}, nil
}

func (p *Google) Complete(ctx context.Context, model string, messages []Message, tools []Tool) (string, error) {
	key, err := p.getKey()
	if err != nil {
		return "", err
	}

	var reqContents []map[string]interface{}
	var systemInstructions map[string]interface{}

	for _, m := range messages {
		if m.Role == "system" {
			systemInstructions = map[string]interface{}{
				"parts": []map[string]string{{"text": m.Content}},
			}
		} else {
			role := "user"
			if m.Role == "assistant" {
				role = "model"
			}
			reqContents = append(reqContents, map[string]interface{}{
				"role":  role,
				"parts": []map[string]string{{"text": m.Content}},
			})
		}
	}

	payload := map[string]interface{}{
		"contents": reqContents,
	}
	if systemInstructions != nil {
		payload["system_instruction"] = systemInstructions
	}

	// Simple tool structure mapping if needed (very basic)
	if len(tools) > 0 {
		var fgTools []map[string]interface{}
		for _, t := range tools {
			fgTools = append(fgTools, map[string]interface{}{
				"name":        t.Name,
				"description": t.Description,
			})
		}
		payload["tools"] = []map[string]interface{}{
			{"function_declarations": fgTools},
		}
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", model, key)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusBadRequest && bytes.Contains(body, []byte("API key not valid")) {
			return "", &ProviderAuthError{ProviderName: "google", Msg: "Unauthorized: Invalid API key"}
		}
		return "", fmt.Errorf("google error: %s (status %d)", string(body), resp.StatusCode)
	}

	var result struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if len(result.Candidates) > 0 && len(result.Candidates[0].Content.Parts) > 0 {
		return result.Candidates[0].Content.Parts[0].Text, nil
	}
	return "", fmt.Errorf("empty response from google")
}
