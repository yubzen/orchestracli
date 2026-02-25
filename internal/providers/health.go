package providers

import "context"

// HealthStatus represents the current state of a provider's connection
type HealthStatus struct {
	Name     string
	IsOnline bool
	ErrorMsg string
}

// CheckAll calls Ping on all provided providers and returns their health statuses
func CheckAll(ctx context.Context, provs []Provider) []HealthStatus {
	var statuses []HealthStatus
	for _, p := range provs {
		err := p.Ping(ctx)
		status := HealthStatus{
			Name:     p.Name(),
			IsOnline: err == nil,
		}
		if err != nil {
			status.ErrorMsg = err.Error()
		}
		statuses = append(statuses, status)
	}
	return statuses
}
