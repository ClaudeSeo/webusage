package domain

import "time"

// CycleType represents the reset cycle type for a provider
type CycleType string

const (
	CycleTypeRolling5h CycleType = "rolling_5h" // 5-hour rolling window (Codex session)
	CycleTypeDaily     CycleType = "daily"      // Daily reset
	CycleTypeWeekly    CycleType = "weekly"     // Weekly reset (7 days)
	CycleTypeMonthly   CycleType = "monthly"    // Monthly reset (billing cycle)
)

// LimitType represents the limit constraint type
type LimitType string

const (
	LimitTypeLimited   LimitType = "limited"
	LimitTypeUnlimited LimitType = "unlimited"
	LimitTypeUnknown   LimitType = "unknown"
)

// ProviderCycleConfig holds cycle-aware configuration for each provider
type ProviderCycleConfig struct {
	CycleType  CycleType `json:"cycle_type"`
	LimitType  LimitType `json:"limit_type"`
	LimitValue *float64  `json:"limit_value,omitempty"`
}

// CurrentCycleInfo represents current cycle state for a provider
type CurrentCycleInfo struct {
	ProviderID           string     `json:"provider_id"`
	DisplayName          string     `json:"display_name"`
	Enabled              bool       `json:"enabled"`
	CycleType            string     `json:"cycle_type"`
	LimitType            string     `json:"limit_type"`
	LimitValue           *float64   `json:"limit_value,omitempty"`
	CurrentUsage         float64    `json:"current_usage"`
	UsagePercent         float64    `json:"usage_percent"`
	CycleStart           *time.Time `json:"cycle_start,omitempty"`
	CycleEnd             *time.Time `json:"cycle_end,omitempty"`
	TimeRemaining        string     `json:"time_remaining,omitempty"`
	ForecastLimitAt      *time.Time `json:"forecast_limit_at,omitempty"`
	WillExceedBeforeReset bool       `json:"will_exceed_before_reset"`
	CurrentPace          float64    `json:"current_pace"`
	BaselinePace        float64    `json:"baseline_pace"`
	PaceVsBaselineRatio float64    `json:"pace_vs_baseline_ratio"`
	LastUpdated          *time.Time `json:"last_updated,omitempty"`
	Error                *string    `json:"error,omitempty"`
}

// TrendDataPoint represents a single data point in trend series
type TrendDataPoint struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
	Metric    string    `json:"metric"`
}

// ProviderTrends represents trend data for a provider
type ProviderTrends struct {
	ProviderID string           `json:"provider_id"`
	CycleType  string           `json:"cycle_type"`
	View       string           `json:"view"`        // current, previous, both
	Mode       string           `json:"mode"`        // absolute, relative, rate
	Bucket     string           `json:"bucket"`      // auto, hour, day, cycle
	Data       []TrendDataPoint `json:"data"`
	CycleStart *time.Time       `json:"cycle_start,omitempty"`
	CycleEnd   *time.Time       `json:"cycle_end,omitempty"`
}

// ForecastInfo represents usage forecast for a provider
type ForecastInfo struct {
	ProviderID            string     `json:"provider_id"`
	DisplayName           string     `json:"display_name"`
	CycleType             string     `json:"cycle_type"`
	CurrentUsage          float64    `json:"current_usage"`
	LimitValue            *float64   `json:"limit_value,omitempty"`
	Remaining             float64    `json:"remaining"`
	CycleEnd              *time.Time `json:"cycle_end,omitempty"`
	ForecastLimitAt       *time.Time `json:"forecast_limit_at,omitempty"`
	WillExceedBeforeReset bool       `json:"will_exceed_before_reset"`
	Confidence            float64    `json:"confidence"`
	HoursUntilLimit       *float64   `json:"hours_until_limit,omitempty"`
	RecommendedAction     string     `json:"recommended_action,omitempty"`
}

// ProviderMetadata represents provider metadata
type ProviderMetadata struct {
	ProviderID       string    `json:"provider_id"`
	DisplayName      string    `json:"display_name"`
	AuthMethod       string    `json:"auth_method"`
	Enabled          bool      `json:"enabled"`
	CycleType        string    `json:"cycle_type"`
	LimitType        string    `json:"limit_type"`
	LimitValue       *float64  `json:"limit_value,omitempty"`
	Metrics          []string  `json:"metrics"`
	SupportedViews   []string  `json:"supported_views"`
	SupportedModes   []string  `json:"supported_modes"`
	SupportedBuckets []string  `json:"supported_buckets"`
}

// MetricView represents a single metric for display
type MetricView struct {
	Name    string    `json:"name"`
	Label   string    `json:"label"`
	Used    float64   `json:"used"`
	Limit   float64   `json:"limit"`
	Percent float64   `json:"percent"`
	ResetAt time.Time `json:"reset_at,omitempty"`
}

// ProviderView represents a provider for dashboard display
type ProviderView struct {
	ID                    int64
	Name                  string
	Enabled               bool
	Metrics               []MetricView
	CollectedAt           time.Time
	UpdatedAt             time.Time
	LastError             *string
	CycleType             string
	LimitType             string
	CycleStartAt          time.Time
	CycleEndAt            time.Time
	TimeRemaining         string
	WillExceedBeforeReset bool
	CurrentPace           float64
	BaselinePace          float64
	PaceRatio            float64
}