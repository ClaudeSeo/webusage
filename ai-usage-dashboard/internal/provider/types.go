package provider

import "time"

// UsagePoint represents a single usage measurement
type UsagePoint struct {
	ProviderID  int64      `json:"provider_id"`
	Metric      string     `json:"metric"`
	Used        float64    `json:"used"`
	Limit       *float64   `json:"limit,omitempty"`
	ResetAt     *time.Time `json:"reset_at,omitempty"`
	CollectedAt time.Time  `json:"collected_at"`
	RawJSON     string     `json:"raw_json"`
}

// ProviderConfig holds configuration for a provider
type ProviderConfig struct {
	APIKey    string `json:"api_key"`
	BaseURL   string `json:"base_url,omitempty"`
	OrgID     string `json:"org_id,omitempty"`
	ProjectID string `json:"project_id,omitempty"`
}

// ProviderStatus tracks the health of a provider connection
type ProviderStatus struct {
	ID        int64      `json:"id"`
	Name      string     `json:"name"`
	Enabled   bool       `json:"enabled"`
	LastRun   *time.Time `json:"last_run,omitempty"`
	LastError *string    `json:"last_error,omitempty"`
}

// InputOutputTokens represents token counts for tracking
type InputOutputTokens struct {
	Input  int `json:"input_tokens"`
	Output int `json:"output_tokens"`
}
