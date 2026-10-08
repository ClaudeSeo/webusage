package ollama

import (
	"context"
	"testing"
	"time"

	"github.com/ClaudeSeo/webusage/internal/native"
)

// num returns a pointer to v, mirroring the API's optional numeric fields.
func num(v float64) *float64 { return &v }

// at parses an RFC 3339 timestamp for test fixtures.
func at(t *testing.T, s string) *time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return &v
}

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

func TestMetricsFromBalanceShouldEmitSpentCreditsWhenPlanHasIncludedCredits(t *testing.T) {
	// Given: a current plan with 40.25 of 50 USD included credits remaining.
	until := at(t, "2026-09-01T00:00:00Z")
	resp := &balanceResponse{
		Included: includedBalance{
			BalanceUSD:   num(40.25),
			AllowanceUSD: num(50),
			Period:       &creditPeriod{From: at(t, "2026-08-01T00:00:00Z"), Until: until},
		},
		Purchased: purchasedBalance{BalanceUSD: num(10)},
	}

	// When
	metrics := metricsFromBalance(resp)

	// Then: the remaining balance is inverted into spend against the allowance,
	// resetting at the period end. Purchased credits stay out of the metrics.
	if len(metrics) != 1 || metrics[0].Metric != "credits" {
		t.Fatalf("metrics = %+v, want a single credits metric", metrics)
	}
	credits := metrics[0]
	if credits.Used != 9.75 {
		t.Errorf("credits used = %v, want 9.75", credits.Used)
	}
	if credits.Limit == nil || *credits.Limit != 50 {
		t.Errorf("credits limit = %v, want 50", credits.Limit)
	}
	if credits.ResetAt == nil || !credits.ResetAt.Equal(*until) {
		t.Errorf("credits reset = %v, want %v", credits.ResetAt, until)
	}
}

func TestMetricsFromBalanceShouldSkipCreditsWhenAllowanceIsNotPositive(t *testing.T) {
	// Given: credit fields present but the allowance is missing or zero.
	for name, allowance := range map[string]*float64{"absent": nil, "zero": num(0)} {
		resp := &balanceResponse{
			Included: includedBalance{BalanceUSD: num(0), AllowanceUSD: allowance},
		}

		// When
		metrics := metricsFromBalance(resp)

		// Then: no limit means no quota position, so nothing is stored as 0%.
		if len(metrics) != 0 {
			t.Errorf("%s: metrics = %+v, want none", name, metrics)
		}
	}
}

func TestMetricsFromBalanceShouldInvertRemainingPercentWhenPlanIsLegacy(t *testing.T) {
	// Given: a legacy plan reporting remaining percentages and reset times.
	sessionReset := at(t, "2026-08-02T12:00:00Z")
	weeklyReset := at(t, "2026-08-06T00:00:00Z")
	resp := &balanceResponse{
		Included: includedBalance{
			Session: &legacyWindow{RemainingPercent: num(60), ResetsAt: sessionReset},
			Weekly:  &legacyWindow{RemainingPercent: num(25), ResetsAt: weeklyReset},
		},
		Purchased: purchasedBalance{BalanceUSD: num(0)},
	}

	// When
	got := byMetric(metricsFromBalance(resp))

	// Then: stored as consumed percent with limit 100 under the existing
	// session/weekly keys, so the stored history stays continuous.
	for _, want := range []struct {
		metric string
		used   float64
		reset  *time.Time
	}{
		{"session", 40, sessionReset},
		{"weekly", 75, weeklyReset},
	} {
		m, ok := got[want.metric]
		if !ok {
			t.Errorf("%s metric missing", want.metric)
			continue
		}
		if m.Used != want.used {
			t.Errorf("%s used = %v, want %v", want.metric, m.Used, want.used)
		}
		if m.Limit == nil || *m.Limit != 100 {
			t.Errorf("%s limit = %v, want 100", want.metric, m.Limit)
		}
		if m.ResetAt == nil || !m.ResetAt.Equal(*want.reset) {
			t.Errorf("%s reset = %v, want %v", want.metric, m.ResetAt, want.reset)
		}
	}
	if len(got) != 2 {
		t.Errorf("metrics = %+v, want only session and weekly", got)
	}
}

func TestMetricsFromBalanceShouldSkipLegacyWindowsThatAreNotReported(t *testing.T) {
	// Given: weekly absent, session present without a remaining value.
	resp := &balanceResponse{
		Included: includedBalance{Session: &legacyWindow{ResetsAt: at(t, "2026-08-02T12:00:00Z")}},
	}

	// When
	metrics := metricsFromBalance(resp)

	// Then: an unreported window is not mistaken for 100% consumed.
	if len(metrics) != 0 {
		t.Errorf("metrics = %+v, want none when no remaining value is reported", metrics)
	}
}

func TestCostMetricShouldEmitTotalSpendWhenUsageIsPriced(t *testing.T) {
	// Given: a usage report carrying a USD total.
	resp := &usageResponse{Totals: usageMetrics{RequestCount: 15, UsageUSD: num(0.01718)}}

	// When
	cost, ok := costMetric(resp)

	// Then: a limit-free spend metric.
	if !ok {
		t.Fatal("cost metric missing")
	}
	if cost.Metric != "cost" || cost.Used != 0.01718 {
		t.Errorf("cost = %+v, want cost used 0.01718", cost)
	}
	if cost.Limit != nil {
		t.Errorf("cost limit = %v, want nil (spend has no cap here)", cost.Limit)
	}
}

func TestCostMetricShouldSkipWhenUsageIsNotPriced(t *testing.T) {
	// Given: legacy requests in the range, which omit usage_usd.
	resp := &usageResponse{Totals: usageMetrics{RequestCount: 7}}

	// When / Then: unpriced usage is not stored as zero spend.
	if cost, ok := costMetric(resp); ok {
		t.Errorf("cost = %+v, want none when usage_usd is absent", cost)
	}
	if _, ok := costMetric(nil); ok {
		t.Error("cost emitted for a nil response")
	}
}

func TestMetricsFromBalanceShouldReturnNothingForEmptyResponse(t *testing.T) {
	// Given / When / Then: a nil response yields no metrics rather than panicking.
	if metrics := metricsFromBalance(nil); len(metrics) != 0 {
		t.Errorf("metrics = %+v, want none for nil response", metrics)
	}
	// Then: an empty payload is "present but no usage yet", not an error.
	if metrics := metricsFromBalance(&balanceResponse{}); len(metrics) != 0 {
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
