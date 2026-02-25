package providers

import (
	"context"
	"testing"
)

type stubDiscovery struct {
	models []string
	err    error
}

func (s stubDiscovery) ListModels(ctx context.Context) ([]string, error) {
	return s.models, s.err
}

func TestValidateCredential(t *testing.T) {
	if err := ValidateCredential("  "); err == nil {
		t.Fatal("expected empty credential error")
	}
	if err := ValidateCredential("token"); err != nil {
		t.Fatalf("expected credential to be valid, got %v", err)
	}
}

func TestDiscoverModelsNormalizesAndSorts(t *testing.T) {
	models, err := DiscoverModels(context.Background(), stubDiscovery{
		models: []string{" gpt-4o ", "claude-3-5-sonnet", "gpt-4o", "", "claude-3-5-sonnet"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := len(models), 2; got != want {
		t.Fatalf("expected %d models, got %d", want, got)
	}
	if models[0] != "claude-3-5-sonnet" || models[1] != "gpt-4o" {
		t.Fatalf("unexpected normalized models: %#v", models)
	}
}
