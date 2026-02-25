package providers

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestFetchOpenRouterModelsMarksFreeModels(t *testing.T) {
	t.Parallel()

	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
				t.Fatalf("unexpected auth header: %q", got)
			}
			if got := r.Header.Get("HTTP-Referer"); got != "https://github.com/orchestra" {
				t.Fatalf("unexpected referer header: %q", got)
			}
			if got := r.Header.Get("X-Title"); got != "orchestra" {
				t.Fatalf("unexpected title header: %q", got)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(strings.NewReader(
					`{"data":[{"id":"meta-llama/llama-3.1-8b-instruct:free","pricing":{"prompt":"0"}},{"id":"openai/gpt-4o","pricing":{"prompt":"0.000005"}}]}`,
				)),
				Header: make(http.Header),
			}, nil
		}),
	}

	models, err := fetchOpenRouterModels(context.Background(), client, "https://openrouter.ai/api/v1/models", "test-key")
	if err != nil {
		t.Fatalf("fetch models: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	if got := models[0]; got != "meta-llama/llama-3.1-8b-instruct:free [free]" {
		t.Fatalf("unexpected first model %q", got)
	}
	if got := models[1]; got != "openai/gpt-4o" {
		t.Fatalf("unexpected second model %q", got)
	}
}

func TestFetchOpenRouterModelsUnauthorized(t *testing.T) {
	t.Parallel()

	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusUnauthorized,
				Body:       io.NopCloser(strings.NewReader("")),
				Header:     make(http.Header),
			}, nil
		}),
	}

	_, err := fetchOpenRouterModels(context.Background(), client, "https://openrouter.ai/api/v1/models", "bad-key")
	if err == nil {
		t.Fatal("expected auth error")
	}
	if got := err.Error(); got != "invalid OpenRouter API key" {
		t.Fatalf("unexpected error: %q", got)
	}
}
