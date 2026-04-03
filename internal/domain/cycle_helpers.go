package domain

import (
	"fmt"
	"sort"
	"time"
)

// ProviderCycleConfigs defines cycle configurations for known providers
// This can be moved to configuration file in the future
var ProviderCycleConfigs = map[string]ProviderCycleConfig{
	"claude": {
		CycleType: CycleTypeRolling5h,
		LimitType: LimitTypeLimited,
	},
	"codex": {
		CycleType: CycleTypeRolling5h,
		LimitType: LimitTypeLimited,
	},
	"copilot": {
		CycleType: CycleTypeMonthly,
		LimitType: LimitTypeLimited,
	},
}

// GetProviderCycleConfig returns cycle config for a provider
func GetProviderCycleConfig(providerName string) ProviderCycleConfig {
	if cfg, ok := ProviderCycleConfigs[providerName]; ok {
		return cfg
	}
	// Default to daily cycle with unknown limit
	return ProviderCycleConfig{
		CycleType: CycleTypeDaily,
		LimitType: LimitTypeUnknown,
	}
}

// CalculateCycleBoundaries calculates the current cycle start/end times based on cycle type
func CalculateCycleBoundaries(cycleType CycleType, now time.Time, resetAt *time.Time) (*time.Time, *time.Time) {
	var start, end time.Time

	switch cycleType {
	case CycleTypeRolling5h:
		// For rolling 5h: cycle spans from (now - 5h) to reset time or now+5h
		if resetAt != nil && resetAt.After(now) {
			start = resetAt.Add(-5 * time.Hour)
			end = *resetAt
		} else {
			start = now.Add(-5 * time.Hour)
			end = now.Add(5 * time.Hour)
		}
	case CycleTypeDaily:
		// Daily: midnight to midnight
		start = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		end = start.Add(24 * time.Hour)
	case CycleTypeWeekly:
		// Weekly: assume Monday start
		weekday := int(now.Weekday())
		if weekday == 0 {
			weekday = 7 // Sunday = 7
		}
		start = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		start = start.Add(-time.Duration(weekday-1) * 24 * time.Hour)
		end = start.Add(7 * 24 * time.Hour)
	case CycleTypeMonthly:
		// Monthly: billing cycle
		if resetAt != nil && resetAt.After(now) {
			// Approximate monthly cycle based on reset date
			end = *resetAt
			start = end.Add(-30 * 24 * time.Hour)
		} else {
			start = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
			end = start.AddDate(0, 1, 0)
		}
	default:
		// Default to daily
		start = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		end = start.Add(24 * time.Hour)
	}

	return &start, &end
}

// FormatDuration formats a duration into human-readable string
func FormatDuration(d time.Duration) string {
	if d < 0 {
		return "0m"
	}
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	if hours > 0 {
		days := hours / 24
		remainingHours := hours % 24
		if days > 0 {
			return fmt.Sprintf("%dd %dh", days, remainingHours)
		}
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}

// CalculatePace calculates usage pace from trend data
// Returns: current pace (per hour), baseline pace, ratio
func CalculatePace(data []TrendDataPoint) (currentPace, baselinePace, ratio float64) {
	if len(data) < 2 {
		return 0, 0, 0
	}

	sorted := make([]TrendDataPoint, len(data))
	copy(sorted, data)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Timestamp.Before(sorted[j].Timestamp)
	})

	// Calculate current pace from recent data points (last 1 hour or 3 points)
	recentPoints := 3
	if len(sorted) < recentPoints {
		recentPoints = len(sorted)
	}
	recentData := sorted[len(sorted)-recentPoints:]
	if len(recentData) >= 2 {
		timeSpan := recentData[len(recentData)-1].Timestamp.Sub(recentData[0].Timestamp).Hours()
		if timeSpan > 0 {
			valueSpan := recentData[len(recentData)-1].Value - recentData[0].Value
			currentPace = valueSpan / timeSpan
		}
	}

	// Calculate baseline pace from all data (overall average)
	totalTimeSpan := sorted[len(sorted)-1].Timestamp.Sub(sorted[0].Timestamp).Hours()
	if totalTimeSpan > 0 {
		totalValueSpan := sorted[len(sorted)-1].Value - sorted[0].Value
		baselinePace = totalValueSpan / totalTimeSpan
	}

	if baselinePace > 0 {
		ratio = currentPace / baselinePace
	} else if currentPace > 0 {
		ratio = 999 // Very high ratio if baseline is near zero
	}

	return currentPace, baselinePace, ratio
}

// ForecastLimitExceedTime forecasts when the limit will be reached
func ForecastLimitExceedTime(currentUsage float64, limitValue *float64, pace float64, cycleEnd *time.Time) (*time.Time, bool) {
	if limitValue == nil || *limitValue <= 0 || pace <= 0 || cycleEnd == nil {
		return nil, false
	}

	remaining := *limitValue - currentUsage
	if remaining <= 0 {
		return nil, true // Already exceeded
	}

	hoursUntilLimit := remaining / pace
	forecastTime := time.Now().Add(time.Duration(hoursUntilLimit) * time.Hour)

	willExceedBeforeReset := forecastTime.Before(*cycleEnd)
	return &forecastTime, willExceedBeforeReset
}

// MetricLabels maps metric keys to display labels (Korean)
var MetricLabels = map[string]string{
	"session":              "세션 (5h)",
	"weekly":               "주간 (7d)",
	"weekly_sonnet":        "주간 Sonnet",
	"extra_credits":        "Extra 크레딧",
	"credits":              "크레딧",
	"premium_interactions": "프리미엄 사용량",
	"chat":                 "채팅",
}

// MetricLabel returns the Korean display label for a metric key
func MetricLabel(metric string) string {
	if label, ok := MetricLabels[metric]; ok {
		return label
	}
	return metric
}