package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type Embedder struct {
	URL    string
	Model  string
	Client *http.Client
}

var ErrEmbedderNotReady = errors.New("rag embedder is not initialized")

func NewEmbedder(url, model string) *Embedder {
	if url == "" {
		url = "http://localhost:11434"
	}
	if model == "" {
		model = "nomic-embed-text"
	}
	return &Embedder{
		URL:    strings.TrimRight(url, "/"),
		Model:  model,
		Client: http.DefaultClient,
	}
}

func (e *Embedder) ensureConfigured() error {
	if e == nil {
		return ErrEmbedderNotReady
	}
	if strings.TrimSpace(e.URL) == "" {
		return errors.New("rag embedder URL is empty")
	}
	if strings.TrimSpace(e.Model) == "" {
		return errors.New("rag embedder model is empty")
	}
	return nil
}

func (e *Embedder) client() *http.Client {
	if e != nil && e.Client != nil {
		return e.Client
	}
	return http.DefaultClient
}

func (e *Embedder) EnsureReady(ctx context.Context) error {
	if err := e.ensureConfigured(); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.URL+"/api/tags", nil)
	if err != nil {
		return fmt.Errorf("build ollama healthcheck request: %w", err)
	}

	resp, err := e.client().Do(req)
	if err != nil {
		return fmt.Errorf("ollama embedder is unreachable at %s: %w", e.URL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("ollama healthcheck failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	return nil
}

func (e *Embedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if err := e.ensureConfigured(); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	reqBody, err := json.Marshal(map[string]interface{}{
		"model":  e.Model,
		"prompt": text,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal ollama request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.URL+"/api/embeddings", bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("ollama embed error: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var res struct {
		Embedding []float32 `json:"embedding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}

	return res.Embedding, nil
}
