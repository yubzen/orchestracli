package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	defaultOpenRouterBaseURL = "https://openrouter.ai/api/v1"
	openRouterModelsURL      = "https://openrouter.ai/api/v1/models"
)

type OpenRouter struct {
	BaseURL string
	KeyName string
	Client  *http.Client
	openai  *OpenAI
}

func NewOpenRouter(baseURL, keyName string) *OpenRouter {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = defaultOpenRouterBaseURL
	}
	if strings.TrimSpace(keyName) == "" {
		keyName = "openrouter"
	}
	baseURL = strings.TrimRight(baseURL, "/")
	openaiCompat := NewOpenAI(baseURL, keyName)
	openaiCompat.ExtraHeaders = map[string]string{
		"HTTP-Referer": "https://github.com/orchestra",
		"X-Title":      "orchestra",
	}
	return &OpenRouter{
		BaseURL: baseURL,
		KeyName: keyName,
		Client:  openaiCompat.Client,
		openai:  openaiCompat,
	}
}

func (p *OpenRouter) Name() string {
	return p.KeyName
}

func (p *OpenRouter) getKey() (string, error) {
	key, err := LoadCredential(p.KeyName)
	if err != nil || strings.TrimSpace(key) == "" {
		return "", &ProviderAuthError{ProviderName: p.KeyName, Msg: "API key not found. Run /connect to reconnect provider."}
	}
	return strings.TrimSpace(key), nil
}

func (p *OpenRouter) Ping(ctx context.Context) error {
	_, err := p.getKey()
	return err
}

func (p *OpenRouter) ListModels(ctx context.Context) ([]string, error) {
	key, err := p.getKey()
	if err != nil {
		return nil, err
	}
	client := p.Client
	if client == nil {
		client = http.DefaultClient
	}
	return fetchOpenRouterModels(ctx, client, openRouterModelsURL, key)
}

func (p *OpenRouter) Complete(ctx context.Context, model string, messages []Message, tools []Tool, onToken TokenCallback) (CompletionResponse, error) {
	if p.openai == nil {
		return CompletionResponse{}, errors.New("openrouter provider is not initialized")
	}
	return p.openai.Complete(ctx, model, messages, tools, onToken)
}

func FetchOpenRouterModels(ctx context.Context, apiKey string) ([]string, error) {
	return fetchOpenRouterModels(ctx, http.DefaultClient, openRouterModelsURL, apiKey)
}

func fetchOpenRouterModels(ctx context.Context, client *http.Client, endpoint, apiKey string) ([]string, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, errors.New("OpenRouter API key is required")
	}
	if client == nil {
		client = http.DefaultClient
	}

	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("HTTP-Referer", "https://github.com/orchestra")
	req.Header.Set("X-Title", "orchestra")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, errors.New("invalid OpenRouter API key")
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openrouter model discovery failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var result struct {
		Data []struct {
			ID      string `json:"id"`
			Pricing struct {
				Prompt string `json:"prompt"`
			} `json:"pricing"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	models := make([]string, 0, len(result.Data))
	for _, model := range result.Data {
		id := strings.TrimSpace(model.ID)
		if id == "" {
			continue
		}
		label := id
		if strings.TrimSpace(model.Pricing.Prompt) == "0" {
			label += " [free]"
		}
		models = append(models, label)
	}
	return models, nil
}
