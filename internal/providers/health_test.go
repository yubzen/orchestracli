package providers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

type MockProvider struct {
	name   string
	errOut error
}

func (m MockProvider) Name() string                                     { return m.name }
func (m MockProvider) Ping(ctx context.Context) error                   { return m.errOut }
func (m MockProvider) ListModels(ctx context.Context) ([]string, error) { return nil, nil }
func (m MockProvider) Complete(ctx context.Context, model string, messages []Message, tools []Tool, onToken TokenCallback) (CompletionResponse, error) {
	return CompletionResponse{}, nil
}

// TestHealthCheck verifies the CheckAll pings providers
func TestHealthCheck(t *testing.T) {
	tests := []struct {
		name          string
		providers     []Provider
		expectedCount int
	}{
		{
			name: "mixed statuses",
			providers: []Provider{
				MockProvider{name: "ok_prov", errOut: nil},
				MockProvider{name: "bad_prov", errOut: &ProviderAuthError{ProviderName: "bad_prov", Msg: "no key"}},
			},
			expectedCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			results := CheckAll(ctx, tt.providers)
			assert.Equal(t, tt.expectedCount, len(results))
			for i, r := range results {
				assert.Equal(t, tt.providers[i].Name(), r.Name)
			}
		})
	}
}
