package http

import (
	"encoding/json"
	nethttp "net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ClaudeSeo/webusage/internal/domain"
	"github.com/ClaudeSeo/webusage/internal/store"
)

type metricPreferenceTrendMetric struct {
	Trend []map[string]interface{} `json:"trend"`
}

type metricPreferenceTrendProvider struct {
	AvailableMetrics []string                               `json:"available_metrics"`
	PrimaryMetric    string                                 `json:"primary_metric"`
	Metrics          map[string]metricPreferenceTrendMetric `json:"metrics"`
}

func requestMetricPreferenceDashboard(t *testing.T, server *Server) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	server.mux.ServeHTTP(recorder, httptest.NewRequest(nethttp.MethodGet, "/", nil))
	if recorder.Code != nethttp.StatusOK {
		t.Fatalf("dashboard status = %d, want 200", recorder.Code)
	}
	return recorder.Body.String()
}

func requestMetricPreferenceTrends(t *testing.T, server *Server, query string) metricPreferenceTrendProvider {
	t.Helper()
	recorder := httptest.NewRecorder()
	server.mux.ServeHTTP(recorder, httptest.NewRequest(nethttp.MethodGet, query, nil))
	if recorder.Code != nethttp.StatusOK {
		t.Fatalf("trends status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var response map[string]metricPreferenceTrendProvider
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode trends response error = %v", err)
	}
	provider, exists := response["claude"]
	if !exists {
		t.Fatalf("claude missing from trends response: %s", recorder.Body.String())
	}
	return provider
}

func assertDashboardMetricOrder(t *testing.T, body string, expected []string) {
	t.Helper()
	previousIndex := -1
	for _, metric := range expected {
		index := strings.Index(body, `data-label="`+metric+`"`)
		if index < 0 {
			t.Fatalf("dashboard metric %q missing", metric)
		}
		if index <= previousIndex {
			t.Fatalf("dashboard metrics not ordered as %#v", expected)
		}
		previousIndex = index
	}
}

func TestDashboardAndTrendsMetricPreferenceShouldDefaultToAlphabeticalVisibleCatalog(t *testing.T) {
	// Given: preference 행 없이 이름 순서가 섞인 최신 metric catalog가 있다.
	server, _ := setupMetricPreferenceTestServer(t)
	providerID, err := server.store.CreateProvider("claude", `{}`)
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}
	now := time.Now().UTC()
	for _, metric := range []string{"zeta", "alpha"} {
		if _, err := server.store.CreateUsageSnapshot(&store.UsageSnapshot{
			ProviderID: providerID, Metric: metric, Used: 1, CollectedAt: now,
		}); err != nil {
			t.Fatalf("CreateUsageSnapshot(%q) error = %v", metric, err)
		}
	}

	// When: dashboard와 all-provider trends를 조회한다.
	dashboard := requestMetricPreferenceDashboard(t, server)
	trends := requestMetricPreferenceTrends(t, server, "/api/trends?range=5h")

	// Then: 양쪽 모두 catalog 이름 오름차순 전체 visible이며 첫 key가 primary다.
	assertDashboardMetricOrder(t, dashboard, []string{"alpha", "zeta"})
	if !reflect.DeepEqual(trends.AvailableMetrics, []string{"alpha", "zeta"}) {
		t.Fatalf("available_metrics = %#v, want [alpha zeta]", trends.AvailableMetrics)
	}
	if trends.PrimaryMetric != "alpha" {
		t.Fatalf("primary_metric = %q, want alpha", trends.PrimaryMetric)
	}
}

func TestDashboardAndTrendsMetricPreferenceShouldShareVisibleOrderAndKeepStrictFirstEmpty(t *testing.T) {
	// Given: 첫 visible metric은 선택 range 밖에 있고 hidden/unavailable 항목이 저장되어 있다.
	server, _ := setupMetricPreferenceTestServer(t)
	providerID, err := server.store.CreateProvider("claude", `{}`)
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}
	now := time.Now().UTC()
	snapshots := []*store.UsageSnapshot{
		{ProviderID: providerID, Metric: "beta", Used: 10, CollectedAt: now.Add(-10 * time.Hour)},
		{ProviderID: providerID, Metric: "alpha", Used: 20, CollectedAt: now.Add(-time.Hour)},
		{ProviderID: providerID, Metric: "gamma", Used: 30, CollectedAt: now.Add(-time.Hour)},
	}
	if err := server.store.CreateUsageSnapshots(snapshots); err != nil {
		t.Fatalf("CreateUsageSnapshots() error = %v", err)
	}
	if err := server.store.SaveMetricPreferences([]store.MetricPreferenceUpdate{{
		ProviderID: providerID,
		Items: []domain.MetricPreferenceItem{
			{Metric: "beta", Visible: true},
			{Metric: "alpha", Visible: false},
			{Metric: "stale", Visible: true},
			{Metric: "gamma", Visible: true},
		},
	}}); err != nil {
		t.Fatalf("SaveMetricPreferences() error = %v", err)
	}

	// When: dashboard와 최근 5시간 all-provider trends를 조회한다.
	dashboard := requestMetricPreferenceDashboard(t, server)
	trends := requestMetricPreferenceTrends(t, server, "/api/trends?range=5h")

	// Then: 동일한 visible 순서만 노출되고 첫 metric은 빈 trend여도 primary로 유지된다.
	assertDashboardMetricOrder(t, dashboard, []string{"beta", "gamma"})
	for _, hidden := range []string{"alpha", "stale"} {
		if strings.Contains(dashboard, `data-label="`+hidden+`"`) {
			t.Fatalf("dashboard rendered hidden or unavailable metric %q", hidden)
		}
	}
	if !reflect.DeepEqual(trends.AvailableMetrics, []string{"beta", "gamma"}) {
		t.Fatalf("available_metrics = %#v, want [beta gamma]", trends.AvailableMetrics)
	}
	if trends.PrimaryMetric != "beta" {
		t.Fatalf("primary_metric = %q, want beta", trends.PrimaryMetric)
	}
	if len(trends.Metrics["beta"].Trend) != 0 {
		t.Fatalf("beta trend = %#v, want empty", trends.Metrics["beta"].Trend)
	}
	if len(trends.Metrics["gamma"].Trend) != 1 {
		t.Fatalf("gamma trend count = %d, want 1", len(trends.Metrics["gamma"].Trend))
	}
	for _, excluded := range []string{"alpha", "stale"} {
		if _, exists := trends.Metrics[excluded]; exists {
			t.Fatalf("trends included hidden or unavailable metric %q", excluded)
		}
	}

	// Then: display preference 조회는 snapshot과 Provider 활성 상태를 변경하지 않는다.
	var snapshotCount int
	if err := server.store.DB().QueryRow(`SELECT COUNT(*) FROM usage_snapshots WHERE provider_id = ?`, providerID).Scan(&snapshotCount); err != nil {
		t.Fatalf("count snapshots error = %v", err)
	}
	provider, err := server.store.GetProvider(providerID)
	if err != nil {
		t.Fatalf("GetProvider() error = %v", err)
	}
	if snapshotCount != len(snapshots) || !provider.Enabled {
		t.Fatalf("display requests mutated state: snapshots=%d enabled=%v", snapshotCount, provider.Enabled)
	}
}
