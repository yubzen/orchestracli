package config

import (
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Defaults struct {
		Mode       string `toml:"mode"`
		WorkingDir string `toml:"working_dir"`
	} `toml:"defaults"`
	Providers struct {
		Anthropic struct {
			DefaultModel string `toml:"default_model"`
		} `toml:"anthropic"`
		Google struct {
			DefaultModel string `toml:"default_model"`
		} `toml:"google"`
		OpenAI struct {
			DefaultModel string `toml:"default_model"`
			BaseURL      string `toml:"base_url"`
		} `toml:"openai"`
	} `toml:"providers"`
	RAG struct {
		Enabled      bool   `toml:"enabled"`
		Embedder     string `toml:"embedder"`
		OllamaURL    string `toml:"ollama_url"`
		ChunkSize    int    `toml:"chunk_size"`
		ChunkOverlap int    `toml:"chunk_overlap"`
	} `toml:"rag"`
}

func GetConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "orchestra", "config.toml")
}

func Load() (*Config, error) {
	path := GetConfigPath()
	var cfg Config

	cfg.Defaults.Mode = "solo"
	cfg.Defaults.WorkingDir = "."
	cfg.Providers.Anthropic.DefaultModel = "claude-3-opus-20240229"
	cfg.Providers.Google.DefaultModel = "gemini-2.5-pro"
	cfg.Providers.OpenAI.DefaultModel = "gpt-4o"
	cfg.Providers.OpenAI.BaseURL = "https://api.openai.com/v1"
	cfg.RAG.Enabled = true
	cfg.RAG.Embedder = "nomic-embed-text"
	cfg.RAG.OllamaURL = "http://localhost:11434"
	cfg.RAG.ChunkSize = 512
	cfg.RAG.ChunkOverlap = 64

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return &cfg, nil
	}
	_, err := toml.DecodeFile(path, &cfg)
	return &cfg, err
}

func (c *Config) Save() error {
	path := GetConfigPath()
	os.MkdirAll(filepath.Dir(path), 0755)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(c)
}
