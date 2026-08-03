package ollama

import (
	"context"
	"testing"

	"github.com/ClaudeSeo/webusage/internal/native"
)

// ratio returns a pointer to v, mirroring the API's nullable usage field.
func ratio(v float64) *float64 { return &v }

// byMetric indexes collected metrics by their canonical key.
func byMetric(metrics []native.Metric) map[string]native.Metric {
	out := make(map[string]native.Metric, len(metrics))
	for _, m := range metrics {
		out[m.Metric] = m
	}
	return out
}

func TestAvailableShouldReflectAPIKeyPresence(t *testing.T) {
	// Given / When / Then: a configured key makes the provider collectable.
	if !New("sk-configured").Available() {
		t.Error("Available() = false, want true when OLLAMA_API_KEY is set")
	}

	// Then: an unset key skips the provider instead of failing collection.
	if New("").Available() {
		t.Error("Available() = true, want false when OLLAMA_API_KEY is empty")
	}

	// Then: a whitespace-only key is treated as unset.
	if New("   ").Available() {
		t.Error("Available() = true, want false for a whitespace-only key")
	}
}

func TestMetricsFromUsageShouldScaleLimitRatiosToPercent(t *testing.T) {
	// Given: the API reports limit consumption as a [0,1] ratio.
	resp := &usageResponse{
		Limits: limits{
			Session: &limitBucket{Usage: ratio(0.02)},
			Weekly:  &limitBucket{Usage: ratio(0.365)},
		},
	}

	// When
	got := byMetric(metricsFromUsage(resp))

	// Then: stored on the dashboard's 0-100 scale with limit 100, so
	// used/limit*100 renders the same percentage the API reports.
	session, ok := got["session"]
	if !ok {
		t.Fatal("session metric missing")
	}
	if session.Used != 2 {
		t.Errorf("session used = %v, want 2", session.Used)
	}
	if session.Limit == nil || *session.Limit != 100 {
		t.Errorf("session limit = %v, want 100", session.Limit)
	}

	weekly, ok := got["weekly"]
	if !ok {
		t.Fatal("weekly metric missing")
	}
	if weekly.Used != 36.5 {
		t.Errorf("weekly used = %v, want 36.5", weekly.Used)
	}
	if weekly.Limit == nil || *weekly.Limit != 100 {
		t.Errorf("weekly limit = %v, want 100", weekly.Limit)
	}
}

func TestMetricsFromUsageShouldEmitZeroWhenLimitReportsNoConsumption(t *testing.T) {
	// Given: session present with an explicit zero (distinct from "not reported").
	resp := &usageResponse{
		Limits: limits{Session: &limitBucket{Usage: ratio(0)}},
	}

	// When
	metrics := metricsFromUsage(resp)

	// Then: a reported zero is stored so the trend keeps a continuous series.
	if len(metrics) != 1 || metrics[0].Metric != "session" {
		t.Fatalf("metrics = %+v, want a single session metric", metrics)
	}
	if metrics[0].Used != 0 {
		t.Errorf("session used = %v, want 0", metrics[0].Used)
	}
}

func TestMetricsFromUsageShouldReportOverLimitWhenRatioExceedsOne(t *testing.T) {
	// Given: an account drawing on purchased extra usage past its plan limit.
	resp := &usageResponse{
		Limits: limits{Weekly: &limitBucket{Usage: ratio(1.2)}},
	}

	// When
	metrics := metricsFromUsage(resp)

	// Then: reported as 120% rather than clamped, so an overrun stays visible.
	if len(metrics) != 1 {
		t.Fatalf("metrics = %+v, want 1", metrics)
	}
	if metrics[0].Used != 120 {
		t.Errorf("weekly used = %v, want 120 (unclamped)", metrics[0].Used)
	}
}

func TestMetricsFromUsageShouldSkipLimitsThatAreNotReported(t *testing.T) {
	// Given: weekly bucket absent, session bucket present but without a usage value.
	resp := &usageResponse{
		Limits: limits{Session: &limitBucket{Usage: nil}},
	}

	// When
	metrics := metricsFromUsage(resp)

	// Then: nothing is persisted, so an unreported limit is not mistaken for 0%.
	if len(metrics) != 0 {
		t.Errorf("metrics = %+v, want none when no usage is reported", metrics)
	}
}

func TestMetricsFromUsageShouldEmitCostFromDecimalString(t *testing.T) {
	// Given: the activity cost arrives as a decimal string.
	resp := &usageResponse{
		Activity: activity{Cost: "1.23450"},
		Limits:   limits{Weekly: &limitBucket{Usage: ratio(0.5)}},
	}

	// When
	got := byMetric(metricsFromUsage(resp))

	// Then: parsed into a limit-free spend metric.
	cost, ok := got["cost"]
	if !ok {
		t.Fatal("cost metric missing")
	}
	if cost.Used != 1.2345 {
		t.Errorf("cost used = %v, want 1.2345", cost.Used)
	}
	if cost.Limit != nil {
		t.Errorf("cost limit = %v, want nil (spend has no cap here)", cost.Limit)
	}
}

func TestMetricsFromUsageShouldSkipCostWhenNotParseable(t *testing.T) {
	// Given: costs that are absent, malformed, or non-finite. ParseFloat accepts
	// the last group, but a NaN or Inf would break JSON encoding downstream.
	costs := map[string]string{
		"absent":    "",
		"malformed": "n/a",
		"nan":       "NaN",
		"inf":       "Inf",
		"neg-inf":   "-Infinity",
	}
	for name, cost := range costs {
		resp := &usageResponse{
			Activity: activity{Cost: cost},
			Limits:   limits{Weekly: &limitBucket{Usage: ratio(0.5)}},
		}

		// When
		got := byMetric(metricsFromUsage(resp))

		// Then: the weekly metric still lands; only cost is dropped.
		if _, exists := got["cost"]; exists {
			t.Errorf("%s: cost metric emitted, want dropped", name)
		}
		if _, exists := got["weekly"]; !exists {
			t.Errorf("%s: weekly metric dropped, want kept", name)
		}
	}
}

func TestMetricsFromUsageShouldReturnNothingForEmptyResponse(t *testing.T) {
	// Given / When / Then: a nil response yields no metrics rather than panicking.
	if metrics := metricsFromUsage(nil); len(metrics) != 0 {
		t.Errorf("metrics = %+v, want none for nil response", metrics)
	}
	// Then: an empty payload is "present but no usage yet", not an error.
	if metrics := metricsFromUsage(&usageResponse{}); len(metrics) != 0 {
		t.Errorf("metrics = %+v, want none for empty response", metrics)
	}
}

func TestCollectShouldReturnErrUnavailableWhenAPIKeyMissing(t *testing.T) {
	// Given: no API key configured.
	o := New("")

	// When
	_, err := o.Collect(context.Background())

	// Then: a distinct unavailable error, and no HTTP call was attempted.
	if err != ErrUnavailable {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
}
