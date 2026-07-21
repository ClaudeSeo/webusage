package http

import (
	"strings"
	"testing"
)

func TestTrendsChartClientShouldReconcileVisibleSelectionWithoutReordering(t *testing.T) {
	// Given: server가 제공한 visible metric 순서와 기존 client 선택값이 있는 dashboard다.
	server, _ := setupMetricPreferenceTestServer(t)
	createMetricPreferenceProvider(t, server, "claude", "alpha", "beta")

	// When: dashboard HTML을 조회한다.
	body := requestMetricPreferenceDashboard(t, server)

	// Then: visible 선택은 유지하고 stale 선택만 primary로 교정한다.
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
	// Given: strict-first trend 응답을 렌더하는 dashboard다.
	server, _ := setupMetricPreferenceTestServer(t)
	createMetricPreferenceProvider(t, server, "claude", "alpha", "beta")

	// When: dashboard HTML을 조회한다.
	body := requestMetricPreferenceDashboard(t, server)

	// Then: 선택 metric의 빈 trend는 다른 metric fallback 없이 명시적인 빈 상태로 렌더한다.
	for _, required := range []string{
		`function showTrendChartEmptyState(message, hint = '')`,
		`function ensureTrendChartCanvas()`,
		`if (sortedLabels.length === 0)`,
		`showTrendChartEmptyState('선택한 항목에 표시할 데이터가 없습니다'`,
	} {
		if !strings.Contains(body, required) {
			t.Fatalf("dashboard missing strict-first empty contract %q", required)
		}
	}

	selectedSource := metricPreferenceFunctionSource(t, body, "getSelectedMetricData", "getSelectedTrend")
	if strings.Contains(selectedSource, "available_metrics.find") || strings.Contains(selectedSource, "metrics.find") {
		t.Fatal("selected metric lookup must not fall back to another metric with trend data")
	}

	renderSource := metricPreferenceFunctionSource(t, body, "renderTrendChart", "ensureTrendChartCanvas")
	emptyIndex := strings.Index(renderSource, "if (sortedLabels.length === 0)")
	chartIndex := strings.Index(renderSource, "new Chart(")
	if emptyIndex < 0 || chartIndex < 0 || emptyIndex > chartIndex {
		t.Fatal("empty trend must be handled before creating a chart")
	}
}
