package http

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
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

func TestDashboardTrendChartShouldBucketSnapshotsIntoFiveMinuteIntervalsUsingLastValue(t *testing.T) {
	// Given: a dashboard whose trend renderer receives raw snapshots.
	server, _ := setupMetricPreferenceTestServer(t)
	createMetricPreferenceProvider(t, server, "claude", "session")

	// When: the dashboard HTML is fetched.
	body := requestMetricPreferenceDashboard(t, server)

	// Then: plotting groups timestamps into five-minute buckets and keeps the latest value in each bucket.
	for _, required := range []string{
		`const FIVE_MINUTE_MS = 5 * 60 * 1000;`,
		`function bucketTrendPoints(points)`,
		`Math.floor(timestamp / FIVE_MINUTE_MS) * FIVE_MINUTE_MS`,
		`lastPoint.timestamp`,
		`lastPoint.value`,
		`const points = normalizeSelectedTrend(data, providerName);`,
		`const bucketedPoints = bucketTrendPoints(points);`,
	} {
		if !strings.Contains(body, required) {
			t.Fatalf("dashboard missing five-minute trend bucket contract %q", required)
		}
	}

	points, err := json.Marshal([]map[string]interface{}{
		{"timestamp": "2026-08-09T00:04:59Z", "value": 25},
		{"timestamp": "2026-08-09T00:01:00Z", "value": 10},
		{"timestamp": "2026-08-09T00:05:01Z", "value": 30},
	})
	if err != nil {
		t.Fatalf("encode raw trend points: %v", err)
	}
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("five-minute bucket behavior requires node: %v", err)
	}
	bucketSource := metricPreferenceFunctionSource(t, body, "bucketTrendPoints", "showTrendChartEmptyState")
	script := fmt.Sprintf(`
const FIVE_MINUTE_MS = 5 * 60 * 1000;
%s
const result = bucketTrendPoints(%s);
if (result.length !== 2) throw new Error('expected two five-minute buckets, got ' + result.length);
if (result[0].timestamp !== %q || result[1].timestamp !== %q) throw new Error('unexpected bucket timestamps: ' + JSON.stringify(result));
if (result[0].value !== 25 || result[1].value !== 30) throw new Error('latest value was not retained: ' + JSON.stringify(result));
`, bucketSource, points, "2026-08-09T00:00:00.000Z", "2026-08-09T00:05:00.000Z")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, nodePath, "-e", script)
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("five-minute bucket behavior timed out: %v", ctx.Err())
	}
	if err != nil {
		t.Fatalf("five-minute bucket behavior failed: %v\n%s", err, strings.TrimSpace(string(output)))
	}
}

func TestDashboardTrendChartShouldRenderLinesOnlyInCumulativeAndDeltaModes(t *testing.T) {
	// Given: a dashboard with cumulative and delta trend modes.
	server, _ := setupMetricPreferenceTestServer(t)
	createMetricPreferenceProvider(t, server, "claude", "session")

	// When: the trend SVG renderer source is inspected.
	body := requestMetricPreferenceDashboard(t, server)
	renderSource := metricPreferenceFunctionSource(t, body, "renderTrendSVG", "renderTrendChart")

	// Then: both modes emit series paths without point circles or bar rectangles.
	if !strings.Contains(renderSource, "if (path) markup += `<path class=\"series-line\"") {
		t.Fatal("trend renderer must emit a series line path")
	}
	if strings.Contains(renderSource, "<circle") {
		t.Fatal("trend renderer must not emit point circles")
	}
	if strings.Contains(renderSource, "<rect") {
		t.Fatal("trend renderer must not emit delta bar rectangles")
	}
	if !strings.Contains(renderSource, "state.mode === 'delta'") {
		t.Fatal("trend renderer must preserve delta mode handling")
	}

	// When: two trend requests complete out of order.
	loadSource := metricPreferenceFunctionSource(t, body, "loadTrendData", "setTrendMode")
	loadSource = "async " + loadSource
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("latest trend request behavior requires node: %v", err)
	}
	script := fmt.Sprintf(`
const state = { range: '7d', trends: {} };
let trendRequestGeneration = 0;
const status = { dataset: {} };
const pending = [];
const persisted = [];
const rendered = [];
const olderPayload = { older: { display_name: 'Older response' } };
const newerPayload = { newer: { display_name: 'Newer response' } };
const document = { getElementById(id) { if (id !== 'trendDataStatus') throw new Error('unexpected element ' + id); return status; } };
const persistUIState = () => persisted.push(state.range);
const reconcileProviderMetricSelections = () => {};
const renderProviderFilters = () => {};
const renderMetricSelectors = () => {};
const renderTrendChart = data => rendered.push(data);
function fetch(url) {
    return new Promise(resolve => pending.push({ url, resolve }));
}
function response(payload) {
    return { ok: true, json: async () => payload };
}
%s
(async () => {
    const olderRequest = loadTrendData('5h');
    const newerRequest = loadTrendData('30d');
    if (pending.length !== 2) throw new Error('expected two deferred trend requests, got ' + pending.length);
    pending[1].resolve(response(newerPayload));
    await newerRequest;
    pending[0].resolve(response(olderPayload));
    await olderRequest;
    if (state.trends !== newerPayload) throw new Error('stale response replaced newer state: ' + JSON.stringify(state.trends));
    if (rendered.length !== 1 || rendered[0] !== newerPayload) throw new Error('rendered payloads were not latest-only: ' + rendered.length);
    if (persisted.length !== 1 || persisted[0] !== '30d') throw new Error('stale response persisted state: ' + JSON.stringify(persisted));
    if (status.dataset.apiState !== 'connected') throw new Error('newer response did not mark trends connected');
})().catch(error => { console.error(error); process.exitCode = 1; });
`, loadSource)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, nodePath, "-e", script)
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("latest trend request behavior timed out: %v", ctx.Err())
	}
	if err != nil {
		t.Fatalf("latest trend request behavior failed: %v\n%s", err, strings.TrimSpace(string(output)))
	}
}
