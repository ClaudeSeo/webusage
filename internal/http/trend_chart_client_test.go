package http

import (
	"strings"
	"testing"
)

func TestTrendsChartClientShouldReconcileVisibleSelectionWithoutReordering(t *testing.T) {
	// Given: a dashboard with the visible metric order from the server and existing client selections.
	server, _ := setupMetricPreferenceTestServer(t)
	createMetricPreferenceProvider(t, server, "claude", "alpha", "beta")

	// When: the dashboard HTML is fetched.
	body := requestMetricPreferenceDashboard(t, server)

	// Then: visible selections are preserved and only stale selections are corrected to the primary.
	for _, required := range []string{
		`function reconcileProviderMetricSelections(data)`,
		`const availableMetrics = Array.isArray(providerObj.available_metrics)`,
		`availableMetrics.includes(currentSelection)`,
		`availableMetrics.includes(providerObj.primary_metric)`,
		`reconcileProviderMetricSelections(data);`,
	} {
		if !strings.Contains(body, required) {
			t.Fatalf("dashboard missing chart selection contract %q", required)
		}
	}

	reconcileSource := metricPreferenceFunctionSource(t, body, "reconcileProviderMetricSelections", "renderProviderFilters")
	for _, forbidden := range []string{"currentMode =", "currentRange ="} {
		if strings.Contains(reconcileSource, forbidden) {
			t.Fatalf("selection reconciliation must preserve mode/range; found %q", forbidden)
		}
	}

	selectorSource := metricPreferenceFunctionSource(t, body, "renderMetricSelectors", "loadTrendData")
	if !strings.Contains(selectorSource, "metrics.forEach(metric =>") {
		t.Fatal("metric selector must consume available_metrics in server order")
	}
	if strings.Contains(selectorSource, ".sort(") {
		t.Fatal("metric selector must not reorder server available_metrics")
	}
}

func TestDashboardTrendChartShouldRenderStrictFirstEmptyState(t *testing.T) {
	// Given: a dashboard that renders a strict-first trend response.
	server, _ := setupMetricPreferenceTestServer(t)
	createMetricPreferenceProvider(t, server, "claude", "alpha", "beta")

	// When: the dashboard HTML is fetched.
	body := requestMetricPreferenceDashboard(t, server)

	// Then: an empty trend for the selected metric renders as an explicit empty state without falling back to another metric.
	for _, required := range []string{
		`function showTrendChartEmptyState(message, hint = '')`,
		`function ensureTrendChartCanvas()`,
		`if (sortedLabels.length === 0)`,
		`이 구간에 수집된 스냅샷이 없습니다`,
	} {
		if !strings.Contains(body, required) {
			t.Fatalf("dashboard missing strict-first empty contract %q", required)
		}
	}

	selectedSource := metricPreferenceFunctionSource(t, body, "getSelectedMetricData", "getSelectedTrend")
	if strings.Contains(selectedSource, "available_metrics.find") || strings.Contains(selectedSource, "metrics.find") {
		t.Fatal("selected metric lookup must not fall back to another metric with trend data")
	}

	renderSource := metricPreferenceFunctionSource(t, body, "renderTrendSVG", "renderTrendChart")
	if !strings.Contains(renderSource, "if (sortedLabels.length === 0)") || !strings.Contains(renderSource, "이 구간에 수집된 스냅샷이 없습니다") {
		t.Fatal("empty trend must be handled by the SVG renderer")
	}
}

func TestDashboardTrendChartShouldNormalizeLimitedMetricsToPercent(t *testing.T) {
	// Given: providers can expose selected metrics with different numeric limits.
	server, _ := setupMetricPreferenceTestServer(t)
	createMetricPreferenceProvider(t, server, "kirocli", "credits")

	// When: the dashboard HTML is fetched.
	body := requestMetricPreferenceDashboard(t, server)

	// Then: limited trend values use a shared percentage scale before cumulative or delta rendering.
	for _, required := range []string{
		`function normalizeSelectedTrend(data, providerName)`,
		`const limit = getSelectedLimit(data, providerName);`,
		`value: (point.value / limit) * 100`,
		`const points = normalizeSelectedTrend(data, providerName);`,
		`normalizedToPercent: row.selectedLimit > 0`,
		`limitValue = 100;`,
		`warningValue = 80;`,
	} {
		if !strings.Contains(body, required) {
			t.Fatalf("dashboard missing limited-metric normalization contract %q", required)
		}
	}
}
