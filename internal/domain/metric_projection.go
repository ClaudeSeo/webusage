package domain

import (
	"math"
	"sort"
	"time"
)

// MetricSeverity is the visual severity used by the dashboard for one metric.
// The values intentionally match the dashboard's CSS state names.
type MetricSeverity string

const (
	MetricSeverityOK     MetricSeverity = "ok"
	MetricSeverityWarn   MetricSeverity = "warn"
	MetricSeverityDanger MetricSeverity = "danger"
)

// MetricProjectionInput contains the metric-local inputs needed to calculate a
// dashboard projection. Snapshots may be in any order; ProjectMetric sorts a
// copy and never mutates the caller's slice.
type MetricProjectionInput struct {
	CycleType CycleType
	Limit     *float64
	ResetAt   *time.Time
	Now       time.Time
	Snapshots []TrendDataPoint
}

// MetricProjection is the deterministic, presentation-neutral result for one
// metric. HasProjection reports that arithmetic was possible even when the
// horizon is too long to trust; HasForecast reports a usable projection after
// the weak-estimate guard is applied.
type MetricProjection struct {
	CycleType  CycleType  `json:"cycle_type"`
	CycleLabel string     `json:"cycle_label"`
	CycleStart *time.Time `json:"cycle_start,omitempty"`
	CycleEnd   *time.Time `json:"cycle_end,omitempty"`

	// TimeRemaining is clamped at zero after a cycle boundary has passed.
	// TimeRemainingText is provided for callers that need the domain's standard
	// human-readable duration without reimplementing the formatting rule.
	TimeRemaining     time.Duration `json:"-"`
	TimeRemainingText string        `json:"time_remaining,omitempty"`

	CurrentUsage   float64 `json:"current_usage"`
	CurrentPercent float64 `json:"current_percent"`
	HasCurrent     bool    `json:"has_current"`

	PacePerHour            float64       `json:"pace_per_hour"`
	HasPace                bool          `json:"has_pace"`
	ObservationWindow      time.Duration `json:"-"`
	ObservationWindowHours float64       `json:"observation_window_hours,omitempty"`

	HoursUntilReset  float64 `json:"hours_until_reset,omitempty"`
	ProjectedUsage   float64 `json:"projected_usage,omitempty"`
	ProjectedPercent float64 `json:"projected_percent,omitempty"`
	HasProjection    bool    `json:"has_projection"`
	HasForecast      bool    `json:"has_forecast"`
	ForecastUsable   bool    `json:"forecast_usable"`
	WeakEstimate     bool    `json:"weak_estimate"`

	Severity MetricSeverity `json:"severity"`
}

// CycleLabel returns the full Korean label used for a cycle badge.
func CycleLabel(cycleType CycleType) string {
	switch cycleType {
	case CycleTypeRolling5h:
		return "5시간 롤링"
	case CycleTypeDaily:
		return "일간"
	case CycleTypeWeekly:
		return "주간"
	case CycleTypeMonthly:
		return "월간"
	default:
		return string(cycleType)
	}
}

// CycleShortLabel returns the compact cycle label used beside a metric name.
func CycleShortLabel(cycleType CycleType) string {
	switch cycleType {
	case CycleTypeRolling5h:
		return "5h"
	case CycleTypeDaily:
		return "일"
	case CycleTypeWeekly:
		return "주"
	case CycleTypeMonthly:
		return "월"
	default:
		return string(cycleType)
	}
}

// LimitTypeLabel returns the Korean label used when a provider's limit
// constraint is shown next to its cycle.
func LimitTypeLabel(limitType LimitType) string {
	switch limitType {
	case LimitTypeLimited:
		return "한도 있음"
	case LimitTypeUnlimited:
		return "한도 없음"
	case LimitTypeUnknown:
		return "한도 미확인"
	default:
		return string(limitType)
	}
}

// ResolveMetricCycleType resolves a cycle for a metric without requiring
// callers to apply one provider-wide cycle to every metric. Known mixed-cycle
// provider metrics are explicit; a supplied fallback is used next, followed
// by the provider's historical default.
func ResolveMetricCycleType(providerName, metric string, fallback ...CycleType) CycleType {
	if cycle, ok := providerMetricCycleTypes[providerName][metric]; ok {
		return cycle
	}

	// Some providers emit metric names with a suffix (for example, "spark
	// weekly"). Keep the resolver useful for those names while preserving the
	// explicit table above as the source of truth for ambiguous names.
	lowerMetric := normalizeMetricName(metric)
	switch {
	case lowerMetric == "session" || lowerMetric == "5h" || containsMetricWord(lowerMetric, "session"):
		return CycleTypeRolling5h
	case lowerMetric == "weekly" || containsMetricWord(lowerMetric, "weekly"):
		return CycleTypeWeekly
	case lowerMetric == "daily" || containsMetricWord(lowerMetric, "daily"):
		return CycleTypeDaily
	case lowerMetric == "monthly" || containsMetricWord(lowerMetric, "monthly"):
		return CycleTypeMonthly
	}

	if len(fallback) > 0 && fallback[0] != "" {
		return fallback[0]
	}
	return GetProviderCycleConfig(providerName).CycleType
}

// GetMetricCycleType is an alias with a concise name for callers that already
// have a provider/metric pair and no provider-level fallback to provide.
func GetMetricCycleType(providerName, metric string) CycleType {
	return ResolveMetricCycleType(providerName, metric)
}

var providerMetricCycleTypes = map[string]map[string]CycleType{
	"claude": {
		"session": CycleTypeRolling5h,
		"weekly":  CycleTypeWeekly,
		"fable":   CycleTypeWeekly,
	},
	"codex": {
		"weekly":       CycleTypeWeekly,
		"spark weekly": CycleTypeWeekly,
	},
	"copilot": {
		"premium_interactions": CycleTypeMonthly,
		"chat":                 CycleTypeMonthly,
	},
	"kirocli": {
		"credits":       CycleTypeMonthly,
		"bonus_credits": CycleTypeMonthly,
		"extra_credits": CycleTypeMonthly,
	},
	"ollama": {
		"session": CycleTypeRolling5h,
		"weekly":  CycleTypeWeekly,
		"cost":    CycleTypeMonthly,
	},
}

// CalculateMetricCycleBoundaries calculates boundaries using the metric's
// reset timestamp when it is a future boundary. This differs from the legacy
// provider helper by honoring custom daily/weekly/monthly reset timestamps too.
func CalculateMetricCycleBoundaries(cycleType CycleType, now time.Time, resetAt *time.Time) (*time.Time, *time.Time) {
	if cycleType == "" {
		cycleType = CycleTypeDaily
	}
	if resetAt != nil && resetAt.After(now) {
		end := *resetAt
		start := metricCycleStart(cycleType, end)
		return &start, &end
	}
	return CalculateCycleBoundaries(cycleType, now, resetAt)
}

func metricCycleStart(cycleType CycleType, end time.Time) time.Time {
	switch cycleType {
	case CycleTypeRolling5h:
		return end.Add(-5 * time.Hour)
	case CycleTypeDaily:
		return end.Add(-24 * time.Hour)
	case CycleTypeWeekly:
		return end.Add(-7 * 24 * time.Hour)
	case CycleTypeMonthly:
		return end.AddDate(0, -1, 0)
	default:
		return end.Add(-24 * time.Hour)
	}
}

// ProjectMetric calculates current usage, pace, reset-aware projection, weak
// estimate status, and visual severity for one metric. It has no clock or I/O
// dependency: callers provide Now, and a zero Now derives from the latest
// snapshot timestamp (or the Unix epoch when there are no snapshots).
func ProjectMetric(input MetricProjectionInput) MetricProjection {
	cycleType := input.CycleType
	if cycleType == "" {
		cycleType = CycleTypeDaily
	}
	points := sortedMetricSnapshots(input.Snapshots)
	now := input.Now
	if now.IsZero() {
		if len(points) > 0 {
			now = points[len(points)-1].Timestamp
		} else {
			now = time.Unix(0, 0).UTC()
		}
	}

	result := MetricProjection{
		CycleType:  cycleType,
		CycleLabel: CycleLabel(cycleType),
		Severity:   MetricSeverityOK,
	}
	result.CycleStart, result.CycleEnd = CalculateMetricCycleBoundaries(cycleType, now, input.ResetAt)
	if result.CycleEnd != nil {
		result.TimeRemaining = result.CycleEnd.Sub(now)
		if result.TimeRemaining < 0 {
			result.TimeRemaining = 0
		}
		result.TimeRemainingText = FormatDuration(result.TimeRemaining)
	}

	if len(points) == 0 {
		return result
	}

	if current, ok := latestFiniteMetricSnapshot(points); ok {
		result.CurrentUsage = current.Value
		result.HasCurrent = true
		result.CurrentPercent = usagePercent(current.Value, input.Limit)
	}

	older, newer, ok := latestMetricPacePair(points)
	if ok {
		window := newer.Timestamp.Sub(older.Timestamp)
		result.ObservationWindow = window
		result.ObservationWindowHours = window.Hours()
		result.PacePerHour = (newer.Value - older.Value) / result.ObservationWindowHours
		result.HasPace = true

		if input.ResetAt != nil {
			result.HoursUntilReset = input.ResetAt.Sub(now).Hours()
			if result.HoursUntilReset < 0 {
				result.HoursUntilReset = 0
			}
			base := newer.Value
			if result.HasCurrent {
				base = result.CurrentUsage
			}
			result.ProjectedUsage = base + result.PacePerHour*result.HoursUntilReset
			result.HasProjection = true
			result.WeakEstimate = result.HoursUntilReset > result.ObservationWindowHours*6
			result.ProjectedPercent = usagePercent(result.ProjectedUsage, input.Limit)
			if !result.WeakEstimate {
				result.HasForecast = true
				result.ForecastUsable = true
			}
		}
	}

	result.Severity = metricSeverity(result.CurrentPercent, result.ProjectedPercent, result.HasForecast, input.Limit)
	return result
}

// CalculateMetricProjection is the verbose alias used by callers that prefer
// calculation-oriented naming.
func CalculateMetricProjection(input MetricProjectionInput) MetricProjection {
	return ProjectMetric(input)
}

func sortedMetricSnapshots(points []TrendDataPoint) []TrendDataPoint {
	sorted := append([]TrendDataPoint(nil), points...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Timestamp.Before(sorted[j].Timestamp)
	})
	return sorted
}

func latestFiniteMetricSnapshot(points []TrendDataPoint) (TrendDataPoint, bool) {
	for i := len(points) - 1; i >= 0; i-- {
		if metricValueFinite(points[i].Value) && !points[i].Timestamp.IsZero() {
			return points[i], true
		}
	}
	return TrendDataPoint{}, false
}

// latestMetricPacePair returns the most recent adjacent pair in the current
// cycle. A decreasing pair is a reset marker and stops the search so pace from
// a previous cycle cannot leak into the new one.
func latestMetricPacePair(points []TrendDataPoint) (TrendDataPoint, TrendDataPoint, bool) {
	for i := len(points) - 1; i > 0; i-- {
		older, newer := points[i-1], points[i]
		if older.Timestamp.IsZero() || newer.Timestamp.IsZero() || !newer.Timestamp.After(older.Timestamp) {
			continue
		}
		if !metricValueFinite(older.Value) || !metricValueFinite(newer.Value) {
			continue
		}
		if newer.Value < older.Value {
			return TrendDataPoint{}, TrendDataPoint{}, false
		}
		return older, newer, true
	}
	return TrendDataPoint{}, TrendDataPoint{}, false
}

func usagePercent(usage float64, limit *float64) float64 {
	if limit == nil || *limit <= 0 || !metricValueFinite(usage) {
		return 0
	}
	return (usage / *limit) * 100
}

func metricSeverity(currentPercent, projectedPercent float64, hasForecast bool, limit *float64) MetricSeverity {
	if currentPercent >= 90 {
		return MetricSeverityDanger
	}
	if hasForecast && limit != nil && *limit > 0 && projectedPercent >= 100 {
		return MetricSeverityDanger
	}
	if currentPercent >= 70 {
		return MetricSeverityWarn
	}
	if hasForecast && limit != nil && *limit > 0 && projectedPercent >= 80 {
		return MetricSeverityWarn
	}
	return MetricSeverityOK
}

func metricValueFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func normalizeMetricName(metric string) string {
	var normalized []byte
	for i := 0; i < len(metric); i++ {
		c := metric[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		normalized = append(normalized, c)
	}
	return string(normalized)
}

func containsMetricWord(metric, word string) bool {
	if metric == word {
		return true
	}
	for i := 0; i+len(word) <= len(metric); i++ {
		if metric[i:i+len(word)] != word {
			continue
		}
		leftBoundary := i == 0 || metric[i-1] == ' ' || metric[i-1] == '_' || metric[i-1] == '-'
		right := i + len(word)
		rightBoundary := right == len(metric) || metric[right] == ' ' || metric[right] == '_' || metric[right] == '-'
		if leftBoundary && rightBoundary {
			return true
		}
	}
	return false
}
