package stats

import (
	"fmt"
	"sort"
	"time"
)

// TrendPoint represents a data point in a trend series
type TrendPoint struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
	Metric    string    `json:"metric"`
}

// ProviderStats aggregates statistics for a provider
type ProviderStats struct {
	ProviderID   int64         `json:"provider_id"`
	ProviderName string        `json:"provider_name"`
	Metrics      []MetricStats `json:"metrics"`
	TimeRange    TimeRange     `json:"time_range"`
}

// MetricStats holds aggregated stats for a single metric
type MetricStats struct {
	Metric  string       `json:"metric"`
	Total   float64      `json:"total"`
	Average float64      `json:"average"`
	Min     float64      `json:"min"`
	Max     float64      `json:"max"`
	Samples int          `json:"samples"`
	Trend   []TrendPoint `json:"trend,omitempty"`
}

// TimeRange defines a time window
type TimeRange struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// RangeType defines preset time ranges
type RangeType string

const (
	Range24h RangeType = "24h"
	Range7d  RangeType = "7d"
	Range30d RangeType = "30d"
)

// ValidRanges lists all valid range types
var ValidRanges = []RangeType{Range24h, Range7d, Range30d}

// IsValidRange checks if a range string is valid
func IsValidRange(rangeStr string) bool {
	for _, r := range ValidRanges {
		if string(r) == rangeStr {
			return true
		}
	}
	return false
}

// GetTimeRange returns start and end times for a range type
func GetTimeRange(rt RangeType) TimeRange {
	now := time.Now()
	var start time.Time

	switch rt {
	case Range24h:
		start = now.Add(-24 * time.Hour)
	case Range7d:
		start = now.Add(-7 * 24 * time.Hour)
	case Range30d:
		start = now.Add(-30 * 24 * time.Hour)
	default:
		start = now.Add(-24 * time.Hour)
	}

	return TimeRange{
		Start: start,
		End:   now,
	}
}

// ParseRangeType parses a string into RangeType with validation
func ParseRangeType(rangeStr string) (RangeType, error) {
	switch rangeStr {
	case "24h":
		return Range24h, nil
	case "7d":
		return Range7d, nil
	case "30d":
		return Range30d, nil
	default:
		return "", fmt.Errorf("invalid range: %s (valid: 24h, 7d, 30d)", rangeStr)
	}
}

// CalculateMetricStats computes statistics from raw values
func CalculateMetricStats(metric string, values []float64, timestamps []time.Time) MetricStats {
	if len(values) == 0 {
		return MetricStats{Metric: metric}
	}

	var total, min, max float64
	min = values[0]
	max = values[0]

	for _, v := range values {
		total += v
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}

	avg := total / float64(len(values))

	stats := MetricStats{
		Metric:  metric,
		Total:   total,
		Average: avg,
		Min:     min,
		Max:     max,
		Samples: len(values),
	}

	// Build trend if timestamps provided
	if len(timestamps) == len(values) {
		for i, v := range values {
			stats.Trend = append(stats.Trend, TrendPoint{
				Timestamp: timestamps[i],
				Value:     v,
				Metric:    metric,
			})
		}
	}

	return stats
}

// AggregateByHour groups values by hour for trend visualization
func AggregateByHour(values []float64, timestamps []time.Time) ([]float64, []time.Time) {
	if len(values) == 0 || len(values) != len(timestamps) {
		return values, timestamps
	}

	hourlyData := make(map[time.Time]float64)

	for i, v := range values {
		hour := timestamps[i].Truncate(time.Hour)
		hourlyData[hour] += v
	}

	var aggValues []float64
	var aggTimes []time.Time

	for t, v := range hourlyData {
		aggTimes = append(aggTimes, t)
		aggValues = append(aggValues, v)
	}

	// Sort by time
	type timeValue struct {
		t time.Time
		v float64
	}
	pairs := make([]timeValue, len(aggTimes))
	for i := range aggTimes {
		pairs[i] = timeValue{aggTimes[i], aggValues[i]}
	}
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].t.Before(pairs[j].t)
	})
	for i, p := range pairs {
		aggTimes[i] = p.t
		aggValues[i] = p.v
	}

	return aggValues, aggTimes
}

// PercentChange calculates percentage change between two values
func PercentChange(old, new float64) float64 {
	if old == 0 {
		if new == 0 {
			return 0
		}
		return 100.0
	}
	return ((new - old) / old) * 100.0
}
