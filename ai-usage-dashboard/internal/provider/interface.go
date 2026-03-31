package provider

import "context"

// Provider defines the interface for AI usage providers
type Provider interface {
	// Name returns the provider identifier
	Name() string
	
	// FetchUsage retrieves current usage data from the provider API
	FetchUsage(ctx context.Context) ([]UsagePoint, error)
	
	// Validate checks if the provider configuration is valid
	Validate() error
}
