package domain

import (
	"math"
	"testing"
	"time"
)

func TestProjectMetricUsesLatestPostResetPair(t *testing.T) {
	// Given: snapshots include a reset followed by a new observation, and are not
	// supplied in chronological order.
	reset := time.Date(2026, 8, 3, 14, 0, 0, 0, time.UTC)
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	limit := 100.0
	points := []TrendDataPoint{
		{Timestamp: time.Date(2026, 8, 3, 13, 0, 0, 0, time.UTC), Value: 8},
		{Timestamp: time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC), Value: 20},
		{Timestamp: time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC), Value: 5},
		{Timestamp: time.Date(2026, 8, 3, 11, 0, 0, 0, time.UTC), Value: 25},
	}

	// When: the metric projection is computed.
	got := ProjectMetric(MetricProjectionInput{
		CycleType: CycleTypeRolling5h,
		Limit:     &limit,
		ResetAt:   &reset,
		Now:       now,
		Snapshots: points,
	})

	// Then: the latest valid pair after the reset determines pace and the latest
	// value is the starting point for the reset-aware projection.
	if !got.HasCurrent || got.CurrentUsage != 8 {
		t.Fatalf("CurrentUsage = %v (has current %v), want 8", got.CurrentUsage, got.HasCurrent)
	}
	if !got.HasPace || math.Abs(got.PacePerHour-3) > 1e-9 {
		t.Fatalf("PacePerHour = %v (has pace %v), want 3", got.PacePerHour, got.HasPace)
	}
	if !got.HasForecast || got.WeakEstimate {
		t.Fatalf("forecast = has %v weak %v, want usable forecast", got.HasForecast, got.WeakEstimate)
	}
	if math.Abs(got.ProjectedUsage-14) > 1e-9 {
		t.Fatalf("ProjectedUsage = %v, want 14", got.ProjectedUsage)
	}
}

func TestProjectMetricDoesNotCarryPaceAcrossReset(t *testing.T) {
	// Given: the most recent observation is a reset, with an older pair that had
	// a positive delta.
	points := []TrendDataPoint{
		{Timestamp: time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC), Value: 20},
		{Timestamp: time.Date(2026, 8, 3, 11, 0, 0, 0, time.UTC), Value: 25},
		{Timestamp: time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC), Value: 5},
	}

	// When: the metric projection is computed.
	got := ProjectMetric(MetricProjectionInput{
		CycleType: CycleTypeWeekly,
		Now:       points[len(points)-1].Timestamp,
		Snapshots: points,
	})

	// Then: a reset does not reuse a pre-reset pace.
	if got.HasPace {
		t.Fatalf("HasPace = true with a trailing reset, want false")
	}
	if got.PacePerHour != 0 {
		t.Fatalf("PacePerHour = %v, want 0", got.PacePerHour)
	}
}

func TestProjectMetricClassifiesSixObservationWindowsAndSeverity(t *testing.T) {
	// Given: one hour of positive observations and a limit of 100 units.
	observedAt := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	limit := 100.0
	points := []TrendDataPoint{
		{Timestamp: observedAt.Add(-time.Hour), Value: 50},
		{Timestamp: observedAt, Value: 60},
	}

	// When: the reset is exactly six observation windows away.
	exact := ProjectMetric(MetricProjectionInput{
		CycleType: CycleTypeDaily,
		Limit:     &limit,
		ResetAt:   timePtr(observedAt.Add(6 * time.Hour)),
		Now:       observedAt,
		Snapshots: points,
	})

	// Then: the boundary is inclusive, the projection is usable, and the
	// projected limit crossing drives danger severity.
	if exact.WeakEstimate {
		t.Fatalf("WeakEstimate = true at exactly 6 observation windows")
	}
	if !exact.HasForecast || math.Abs(exact.ProjectedUsage-120) > 1e-9 {
		t.Fatalf("forecast = has %v usage %v, want true and 120", exact.HasForecast, exact.ProjectedUsage)
	}
	if math.Abs(exact.ProjectedPercent-120) > 1e-9 {
		t.Fatalf("ProjectedPercent = %v, want 120", exact.ProjectedPercent)
	}
	if exact.Severity != MetricSeverityDanger {
		t.Fatalf("Severity = %q, want %q", exact.Severity, MetricSeverityDanger)
	}

	// When: the reset horizon exceeds six observation windows.
	weak := ProjectMetric(MetricProjectionInput{
		CycleType: CycleTypeDaily,
		Limit:     &limit,
		ResetAt:   timePtr(observedAt.Add(7 * time.Hour)),
		Now:       observedAt,
		Snapshots: points,
	})

	// Then: weak estimates do not affect severity.
	if !weak.WeakEstimate {
		t.Fatalf("WeakEstimate = false beyond 6 observation windows")
	}
	if !weak.HasProjection || math.Abs(weak.ProjectedUsage-130) > 1e-9 || math.Abs(weak.ProjectedPercent-130) > 1e-9 {
		t.Fatalf("weak projection = has %v usage %v percent %v, want true, 130, and 130", weak.HasProjection, weak.ProjectedUsage, weak.ProjectedPercent)
	}
	if !weak.HasProjection || weak.HasForecast || weak.ForecastUsable {
		t.Fatalf("forecast = projection %v forecast %v usable %v, want projection only", weak.HasProjection, weak.HasForecast, weak.ForecastUsable)
	}
	if weak.Severity != MetricSeverityOK {
		t.Fatalf("Severity = %q, want %q when the weak projection is ignored", weak.Severity, MetricSeverityOK)
	}
}

func TestProjectMetricShouldWidenObservationWindowToWholeCycleWhenSnapshotsAccumulate(t *testing.T) {
	// Given: a weekly metric collected every five minutes for the past 24 hours,
	// with the reset still 100 hours away. Usage grows by exactly one unit per
	// hour, so the pace is identical whether it is read from the last adjacent
	// pair or from the whole cycle — only the observation window differs.
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	reset := now.Add(100 * time.Hour)
	limit := 200.0
	const steps = 288 // 24 hours at a 5-minute collection interval
	points := make([]TrendDataPoint, 0, steps+1)
	for i := 0; i <= steps; i++ {
		points = append(points, TrendDataPoint{
			Timestamp: now.Add(-24 * time.Hour).Add(time.Duration(i) * 5 * time.Minute),
			Value:     10 + float64(i)/12,
		})
	}

	// When: the metric projection is computed.
	got := ProjectMetric(MetricProjectionInput{
		CycleType: CycleTypeWeekly,
		Limit:     &limit,
		ResetAt:   &reset,
		Now:       now,
		Snapshots: points,
	})

	// Then: the observation window spans the collected cycle rather than the
	// 5-minute collection interval, so the projection is no longer a weak
	// estimate and is used for severity.
	if math.Abs(got.ObservationWindowHours-24) > 1e-9 {
		t.Fatalf("ObservationWindowHours = %v, want 24", got.ObservationWindowHours)
	}
	if got.WeakEstimate {
		t.Fatalf("WeakEstimate = true with a 24-hour window and a 100-hour horizon")
	}
	if !got.HasForecast || !got.ForecastUsable {
		t.Fatalf("forecast = has %v usable %v, want a usable forecast", got.HasForecast, got.ForecastUsable)
	}
	if math.Abs(got.PacePerHour-1) > 1e-9 {
		t.Fatalf("PacePerHour = %v, want 1", got.PacePerHour)
	}
	if math.Abs(got.ProjectedUsage-134) > 1e-9 {
		t.Fatalf("ProjectedUsage = %v, want 134", got.ProjectedUsage)
	}
}

func TestProjectMetricShouldMeasurePaceFromResetMarkerWhenCycleContainsAReset(t *testing.T) {
	// Given: a reset followed by three observations whose overall slope differs
	// from the slope of the final adjacent pair.
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	reset := now.Add(100 * time.Hour)
	limit := 100.0
	points := []TrendDataPoint{
		{Timestamp: now.Add(-4 * time.Hour), Value: 30},
		{Timestamp: now.Add(-3 * time.Hour), Value: 35},
		{Timestamp: now.Add(-2 * time.Hour), Value: 2}, // reset marker
		{Timestamp: now.Add(-time.Hour), Value: 10},
		{Timestamp: now, Value: 12},
	}

	// When: the metric projection is computed.
	got := ProjectMetric(MetricProjectionInput{
		CycleType: CycleTypeWeekly,
		Limit:     &limit,
		ResetAt:   &reset,
		Now:       now,
		Snapshots: points,
	})

	// Then: the window starts at the reset marker, not at the oldest snapshot and
	// not at the final adjacent pair.
	if math.Abs(got.ObservationWindowHours-2) > 1e-9 {
		t.Fatalf("ObservationWindowHours = %v, want 2", got.ObservationWindowHours)
	}
	if !got.HasPace || math.Abs(got.PacePerHour-5) > 1e-9 {
		t.Fatalf("PacePerHour = %v (has pace %v), want 5", got.PacePerHour, got.HasPace)
	}
}

func TestProjectMetricShouldNotCountACollectionGapOlderThanTheCycleAsObservation(t *testing.T) {
	// Given: collection stopped for 21 days and resumed 5 minutes ago, so the
	// only adjacent pair inside the 5-hour cycle is 5 minutes wide. Usage never
	// decreases, so there is no reset marker to bound the window.
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	reset := now.Add(4 * time.Hour) // cycle started one hour ago
	limit := 100.0
	points := []TrendDataPoint{
		{Timestamp: now.Add(-21 * 24 * time.Hour), Value: 5},
		{Timestamp: now.Add(-5 * time.Minute), Value: 8},
		{Timestamp: now, Value: 9},
	}

	// When: the metric projection is computed.
	got := ProjectMetric(MetricProjectionInput{
		CycleType: CycleTypeRolling5h,
		Limit:     &limit,
		ResetAt:   &reset,
		Now:       now,
		Snapshots: points,
	})

	// Then: the pre-cycle snapshot is excluded, so the gap cannot pose as a long
	// observation and the estimate stays weak.
	if math.Abs(got.ObservationWindowHours-(5.0/60.0)) > 1e-9 {
		t.Fatalf("ObservationWindowHours = %v, want %v", got.ObservationWindowHours, 5.0/60.0)
	}
	if !got.WeakEstimate {
		t.Fatalf("WeakEstimate = false, want true for a 5-minute window and a 4-hour horizon")
	}
	if got.HasForecast || got.ForecastUsable {
		t.Fatalf("forecast = has %v usable %v, want no usable forecast", got.HasForecast, got.ForecastUsable)
	}
}

func TestProjectMetricShouldKeepObservedPaceWhenCalculatedCycleStartIsInTheFuture(t *testing.T) {
	// Given: a 5-hour rolling metric whose provider reports a reset further out
	// than the cycle length, so the calculated cycle start (reset - 5h) lands one
	// hour in the future and precedes no snapshot at all.
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	reset := now.Add(6 * time.Hour)
	limit := 100.0
	points := []TrendDataPoint{
		{Timestamp: now.Add(-time.Hour), Value: 20},
		{Timestamp: now, Value: 40},
	}

	// When: the metric projection is computed.
	got := ProjectMetric(MetricProjectionInput{
		CycleType: CycleTypeRolling5h,
		Limit:     &limit,
		ResetAt:   &reset,
		Now:       now,
		Snapshots: points,
	})

	// Then: the observed snapshots still produce a pace and projection, because a
	// calculated boundary must not discard every observation.
	if !got.HasPace || math.Abs(got.PacePerHour-20) > 1e-9 {
		t.Fatalf("PacePerHour = %v (has pace %v), want 20", got.PacePerHour, got.HasPace)
	}
	if math.Abs(got.ObservationWindowHours-1) > 1e-9 {
		t.Fatalf("ObservationWindowHours = %v, want 1", got.ObservationWindowHours)
	}
	if !got.HasProjection || math.Abs(got.ProjectedUsage-160) > 1e-9 {
		t.Fatalf("projection = has %v usage %v, want true and 160", got.HasProjection, got.ProjectedUsage)
	}
}

func TestProjectMetricProvidesCycleLabelAndRemainingTime(t *testing.T) {
	// Given: a metric with its own weekly reset boundary.
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	reset := now.Add(48 * time.Hour)

	// When: the metric projection is computed.
	got := ProjectMetric(MetricProjectionInput{
		CycleType: CycleTypeWeekly,
		ResetAt:   &reset,
		Now:       now,
		Snapshots: []TrendDataPoint{{Timestamp: now, Value: 1}},
	})

	// Then: cycle metadata is based on this metric's reset, not a provider-wide
	// boundary.
	if got.CycleLabel != "주간" {
		t.Fatalf("CycleLabel = %q, want 주간", got.CycleLabel)
	}
	if got.CycleEnd == nil || !got.CycleEnd.Equal(reset) {
		t.Fatalf("CycleEnd = %v, want %v", got.CycleEnd, reset)
	}
	if got.TimeRemaining != 48*time.Hour {
		t.Fatalf("TimeRemaining = %v, want 48h", got.TimeRemaining)
	}
}

func TestProjectMetricDoesNotForecastWithoutResetOrLimit(t *testing.T) {
	// Given: a valid pace but no reset timestamp and no numeric limit.
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	got := ProjectMetric(MetricProjectionInput{
		CycleType: CycleTypeMonthly,
		Now:       now,
		Snapshots: []TrendDataPoint{
			{Timestamp: now.Add(-time.Hour), Value: 2},
			{Timestamp: now, Value: 3},
		},
	})

	// Then: pace remains available for display, but there is no limit forecast or
	// percent-based severity.
	if !got.HasPace || got.PacePerHour != 1 {
		t.Fatalf("pace = has %v value %v, want true and 1", got.HasPace, got.PacePerHour)
	}
	if got.HasForecast || got.ProjectedUsage != 0 || got.ProjectedPercent != 0 {
		t.Fatalf("forecast = has %v usage %v percent %v, want no forecast", got.HasForecast, got.ProjectedUsage, got.ProjectedPercent)
	}
	if got.Severity != MetricSeverityOK {
		t.Fatalf("Severity = %q, want %q", got.Severity, MetricSeverityOK)
	}
}

func TestResolveMetricCycleTypeUsesMetricSpecificCyclesBeforeProviderFallback(t *testing.T) {
	// Given: providers whose headline metric and secondary metric use different
	// reset windows.
	tests := []struct {
		provider string
		metric   string
		fallback CycleType
		want     CycleType
	}{
		{provider: "claude", metric: "session", fallback: CycleTypeWeekly, want: CycleTypeRolling5h},
		{provider: "claude", metric: "fable", fallback: CycleTypeRolling5h, want: CycleTypeWeekly},
		{provider: "codex", metric: "spark weekly", fallback: CycleTypeRolling5h, want: CycleTypeWeekly},
		{provider: "ollama", metric: "cost", fallback: CycleTypeWeekly, want: CycleTypeMonthly},
		{provider: "unknown", metric: "weekly bonus", fallback: CycleTypeDaily, want: CycleTypeWeekly},
		{provider: "unknown", metric: "other", fallback: CycleTypeMonthly, want: CycleTypeMonthly},
	}

	for _, tt := range tests {
		t.Run(tt.provider+"/"+tt.metric, func(t *testing.T) {
			if got := ResolveMetricCycleType(tt.provider, tt.metric, tt.fallback); got != tt.want {
				t.Fatalf("ResolveMetricCycleType(%q, %q, %q) = %q, want %q", tt.provider, tt.metric, tt.fallback, got, tt.want)
			}
		})
	}
}

func timePtr(t time.Time) *time.Time { return &t }

func TestLimitTypeLabelShouldReturnKoreanLabelsForKnownTypes(t *testing.T) {
	// Given: every limit type the cycle configuration can produce.
	// When: each is converted to a display label.
	// Then: known types read as Korean and unknown values fall back to the raw value.
	for _, testCase := range []struct {
		limitType LimitType
		want      string
	}{
		{limitType: LimitTypeLimited, want: "한도 있음"},
		{limitType: LimitTypeUnlimited, want: "한도 없음"},
		{limitType: LimitTypeUnknown, want: "한도 미확인"},
		{limitType: LimitType("custom"), want: "custom"},
	} {
		if got := LimitTypeLabel(testCase.limitType); got != testCase.want {
			t.Errorf("LimitTypeLabel(%q) = %q, want %q", testCase.limitType, got, testCase.want)
		}
	}
}
