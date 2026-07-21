package http

import (
	"bytes"
	"encoding/json"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ClaudeSeo/webusage/internal/domain"
	"github.com/ClaudeSeo/webusage/internal/store"
)

type metricPreferenceTestProvider struct {
	ProviderID string                                  `json:"provider_id"`
	Version    int64                                   `json:"version"`
	Items      []domain.ReconciledMetricPreferenceItem `json:"items"`
}

type metricPreferenceTestResponse struct {
	Error     string                         `json:"error"`
	Providers []metricPreferenceTestProvider `json:"providers"`
}

func setupMetricPreferenceTestServer(t *testing.T) (*Server, *bytes.Buffer) {
	t.Helper()

	db, err := store.NewStore(filepath.Join(t.TempDir(), "metric-preferences.db"))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	server, err := NewServer(db, "127.0.0.1", 8080, logger, "../../templates")
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	return server, logs
}

func createMetricPreferenceProvider(t *testing.T, server *Server, name string, metrics ...string) int64 {
	t.Helper()

	providerID, err := server.store.CreateProvider(name, `{}`)
	if err != nil {
		t.Fatalf("CreateProvider(%q) error = %v", name, err)
	}
	for index, metric := range metrics {
		_, err := server.store.CreateUsageSnapshot(&store.UsageSnapshot{
			ProviderID:  providerID,
			Metric:      metric,
			Used:        float64(index + 1),
			CollectedAt: time.Now().UTC(),
		})
		if err != nil {
			t.Fatalf("CreateUsageSnapshot(%q) error = %v", metric, err)
		}
	}
	return providerID
}

func performMetricPreferenceRequest(server *Server, method, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/api/metric-preferences", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.mux.ServeHTTP(recorder, req)
	return recorder
}

func decodeMetricPreferenceResponse(t *testing.T, recorder *httptest.ResponseRecorder) metricPreferenceTestResponse {
	t.Helper()

	var response metricPreferenceTestResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response %q error = %v", recorder.Body.String(), err)
	}
	return response
}

func findMetricPreferenceProvider(t *testing.T, providers []metricPreferenceTestProvider, name string) metricPreferenceTestProvider {
	t.Helper()

	for _, provider := range providers {
		if provider.ProviderID == name {
			return provider
		}
	}
	t.Fatalf("provider %q missing from %#v", name, providers)
	return metricPreferenceTestProvider{}
}

func TestMetricPreferenceGETShouldReturnCanonicalSettingsForEveryProvider(t *testing.T) {
	// Given: 저장 순서, unavailable item, 신규 catalog item과 data-less Provider가 있다.
	server, _ := setupMetricPreferenceTestServer(t)
	claudeID := createMetricPreferenceProvider(t, server, "claude", "session", "weekly")
	createMetricPreferenceProvider(t, server, "codex")
	if err := server.store.SaveMetricPreferences([]store.MetricPreferenceUpdate{{
		ProviderID: claudeID,
		Items: []domain.MetricPreferenceItem{
			{Metric: "weekly", Visible: false},
			{Metric: "extra_credits", Visible: true},
		},
	}}); err != nil {
		t.Fatalf("SaveMetricPreferences() error = %v", err)
	}

	// When: 전체 metric preference를 조회한다.
	recorder := performMetricPreferenceRequest(server, nethttp.MethodGet, "")

	// Then: domain label과 availability를 계산한 canonical 설정 및 빈 Provider가 반환된다.
	if recorder.Code != nethttp.StatusOK {
		t.Fatalf("GET status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	response := decodeMetricPreferenceResponse(t, recorder)
	if len(response.Providers) != 2 {
		t.Fatalf("provider count = %d, want 2", len(response.Providers))
	}
	claude := findMetricPreferenceProvider(t, response.Providers, "claude")
	wantClaudeItems := []domain.ReconciledMetricPreferenceItem{
		{Metric: "weekly", Label: "주간 (7d)", Visible: false, Available: true},
		{Metric: "extra_credits", Label: "Extra 크레딧", Visible: true, Available: false},
		{Metric: "session", Label: "세션 (5h)", Visible: true, Available: true},
	}
	if claude.Version != 1 || !reflect.DeepEqual(claude.Items, wantClaudeItems) {
		t.Fatalf("claude = %#v, want version 1 items %#v", claude, wantClaudeItems)
	}
	codex := findMetricPreferenceProvider(t, response.Providers, "codex")
	if codex.Version != 0 || codex.Items == nil || len(codex.Items) != 0 {
		t.Fatalf("codex = %#v, want version 0 with non-nil empty items", codex)
	}
}

func TestMetricPreferencePUTShouldUpdateOnlySubmittedProvidersAndReturnCanonicalSettings(t *testing.T) {
	// Given: 신규 catalog metric이 있는 Provider와 별도 저장 설정이 있는 미제출 Provider가 있다.
	server, _ := setupMetricPreferenceTestServer(t)
	claudeID := createMetricPreferenceProvider(t, server, "claude", "weekly", "session", "extra_credits")
	codexID := createMetricPreferenceProvider(t, server, "codex", "session")
	codexItems := []domain.MetricPreferenceItem{{Metric: "session", Visible: true}}
	if err := server.store.SaveMetricPreferences([]store.MetricPreferenceUpdate{{ProviderID: codexID, Items: codexItems}}); err != nil {
		t.Fatalf("seed codex preference error = %v", err)
	}
	body := `{"providers":[{"provider_id":"claude","expected_version":0,"items":[{"metric":"weekly","visible":false},{"metric":"session","visible":true}]}]}`

	// When: claude 설정만 저장한다.
	recorder := performMetricPreferenceRequest(server, nethttp.MethodPut, body)

	// Then: 새 current metric이 visible로 합성되고 claude만 version 1이 된다.
	if recorder.Code != nethttp.StatusOK {
		t.Fatalf("PUT status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	response := decodeMetricPreferenceResponse(t, recorder)
	if len(response.Providers) != 1 {
		t.Fatalf("response provider count = %d, want 1", len(response.Providers))
	}
	claude := findMetricPreferenceProvider(t, response.Providers, "claude")
	wantItems := []domain.ReconciledMetricPreferenceItem{
		{Metric: "weekly", Label: "주간 (7d)", Visible: false, Available: true},
		{Metric: "session", Label: "세션 (5h)", Visible: true, Available: true},
		{Metric: "extra_credits", Label: "Extra 크레딧", Visible: true, Available: true},
	}
	if claude.Version != 1 || !reflect.DeepEqual(claude.Items, wantItems) {
		t.Fatalf("claude response = %#v, want version 1 items %#v", claude, wantItems)
	}
	savedClaude, err := server.store.GetMetricPreference(claudeID)
	if err != nil {
		t.Fatalf("GetMetricPreference(claude) error = %v", err)
	}
	if savedClaude.Version != 1 || len(savedClaude.Items) != 3 {
		t.Fatalf("saved claude = %#v, want version 1 with synthesized item", savedClaude)
	}
	savedCodex, err := server.store.GetMetricPreference(codexID)
	if err != nil {
		t.Fatalf("GetMetricPreference(codex) error = %v", err)
	}
	if savedCodex.Version != 1 || !reflect.DeepEqual(savedCodex.Items, codexItems) {
		t.Fatalf("unsubmitted codex changed: %#v", savedCodex)
	}
}

func TestMetricPreferencePUTShouldPreserveOmittedUnavailableItemsInTheirSavedSlots(t *testing.T) {
	// Given: available 항목 사이에 unavailable hidden 항목이 저장되어 있다.
	server, _ := setupMetricPreferenceTestServer(t)
	providerID := createMetricPreferenceProvider(t, server, "claude", "session", "weekly")
	storedItems := []domain.MetricPreferenceItem{
		{Metric: "session", Visible: true},
		{Metric: "extra_credits", Visible: false},
		{Metric: "weekly", Visible: true},
	}
	if err := server.store.SaveMetricPreferences([]store.MetricPreferenceUpdate{{ProviderID: providerID, Items: storedItems}}); err != nil {
		t.Fatalf("seed preference error = %v", err)
	}
	body := `{"providers":[{"provider_id":"claude","expected_version":1,"items":[{"metric":"weekly","visible":false},{"metric":"session","visible":true}]}]}`

	// When: client가 unavailable 항목을 생략하고 available 항목만 재정렬해 저장한다.
	recorder := performMetricPreferenceRequest(server, nethttp.MethodPut, body)

	// Then: unavailable 항목은 원래 내부 slot과 visibility를 보존한다.
	if recorder.Code != nethttp.StatusOK {
		t.Fatalf("PUT status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	wantItems := []domain.MetricPreferenceItem{
		{Metric: "weekly", Visible: false},
		{Metric: "extra_credits", Visible: false},
		{Metric: "session", Visible: true},
	}
	preference, err := server.store.GetMetricPreference(providerID)
	if err != nil {
		t.Fatalf("GetMetricPreference() error = %v", err)
	}
	if preference.Version != 2 || !reflect.DeepEqual(preference.Items, wantItems) {
		t.Fatalf("saved preference = %#v, want version 2 items %#v", preference, wantItems)
	}
	response := decodeMetricPreferenceResponse(t, recorder)
	provider := findMetricPreferenceProvider(t, response.Providers, "claude")
	if len(provider.Items) != 3 || provider.Items[1].Metric != "extra_credits" || provider.Items[1].Visible || provider.Items[1].Available {
		t.Fatalf("canonical unavailable item not preserved: %#v", provider.Items)
	}
}

func TestMetricPreferencePUTShouldRejectInvalidPayloadWithoutChangingState(t *testing.T) {
	tests := []struct {
		name  string
		body  func(deletedProvider string) string
		setup func(t *testing.T, server *Server) string
	}{
		{
			name: "body over 64 KiB",
			body: func(string) string { return strings.Repeat(" ", 64*1024+1) + `{"providers":[]}` },
		},
		{
			name: "unknown JSON field",
			body: func(string) string {
				return `{"providers":[{"provider_id":"claude","expected_version":1,"items":[{"metric":"session","visible":true,"label":"forbidden"}]}]}`
			},
		},
		{
			name: "duplicate provider",
			body: func(string) string {
				return `{"providers":[{"provider_id":"claude","expected_version":1,"items":[{"metric":"session","visible":true}]},{"provider_id":"claude","expected_version":1,"items":[{"metric":"session","visible":true}]}]}`
			},
		},
		{
			name: "unknown provider",
			body: func(string) string { return `{"providers":[{"provider_id":"ghost","expected_version":0,"items":[]}]}` },
		},
		{
			name: "deleted provider",
			setup: func(t *testing.T, server *Server) string {
				id := createMetricPreferenceProvider(t, server, "deleted", "session")
				if err := server.store.DeleteProvider(id); err != nil {
					t.Fatalf("DeleteProvider() error = %v", err)
				}
				return "deleted"
			},
			body: func(deletedProvider string) string {
				return `{"providers":[{"provider_id":"` + deletedProvider + `","expected_version":0,"items":[]}]}`
			},
		},
		{
			name: "empty metric key",
			body: func(string) string {
				return `{"providers":[{"provider_id":"claude","expected_version":1,"items":[{"metric":"","visible":true}]}]}`
			},
		},
		{
			name: "duplicate metric key",
			body: func(string) string {
				return `{"providers":[{"provider_id":"claude","expected_version":1,"items":[{"metric":"session","visible":true},{"metric":"session","visible":true}]}]}`
			},
		},
		{
			name: "unknown metric key",
			body: func(string) string {
				return `{"providers":[{"provider_id":"claude","expected_version":1,"items":[{"metric":"not_in_catalog_or_saved","visible":true}]}]}`
			},
		},
		{
			name: "no available visible metric",
			body: func(string) string {
				return `{"providers":[{"provider_id":"claude","expected_version":1,"items":[{"metric":"session","visible":false}]}]}`
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given: version 1 설정과 각 invalid payload가 있다.
			server, _ := setupMetricPreferenceTestServer(t)
			claudeID := createMetricPreferenceProvider(t, server, "claude", "session")
			baselineItems := []domain.MetricPreferenceItem{{Metric: "session", Visible: true}}
			if err := server.store.SaveMetricPreferences([]store.MetricPreferenceUpdate{{ProviderID: claudeID, Items: baselineItems}}); err != nil {
				t.Fatalf("seed preference error = %v", err)
			}
			deletedProvider := ""
			if test.setup != nil {
				deletedProvider = test.setup(t, server)
			}

			// When: invalid payload를 제출한다.
			recorder := performMetricPreferenceRequest(server, nethttp.MethodPut, test.body(deletedProvider))

			// Then: HTTP 400이며 기존 설정이 불변이다.
			if recorder.Code != nethttp.StatusBadRequest {
				t.Fatalf("PUT status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
			}
			preference, err := server.store.GetMetricPreference(claudeID)
			if err != nil {
				t.Fatalf("GetMetricPreference() error = %v", err)
			}
			if preference.Version != 1 || !reflect.DeepEqual(preference.Items, baselineItems) {
				t.Fatalf("preference changed after invalid payload: %#v", preference)
			}
		})
	}
}

func TestMetricPreferencePUTShouldRollbackAndReturnLatestCanonicalOnVersionConflict(t *testing.T) {
	// Given: 두 Provider의 version 1 설정과 두 번째 Provider가 stale인 payload가 있다.
	server, logs := setupMetricPreferenceTestServer(t)
	claudeID := createMetricPreferenceProvider(t, server, "claude", "session", "weekly")
	codexID := createMetricPreferenceProvider(t, server, "codex", "weekly")
	claudeItems := []domain.MetricPreferenceItem{
		{Metric: "session", Visible: true},
		{Metric: "weekly", Visible: true},
	}
	codexItems := []domain.MetricPreferenceItem{{Metric: "weekly", Visible: true}}
	if err := server.store.SaveMetricPreferences([]store.MetricPreferenceUpdate{
		{ProviderID: claudeID, Items: claudeItems},
		{ProviderID: codexID, Items: codexItems},
	}); err != nil {
		t.Fatalf("seed preferences error = %v", err)
	}
	body := `{"providers":[{"provider_id":"claude","expected_version":1,"items":[{"metric":"session","visible":false},{"metric":"weekly","visible":true}]},{"provider_id":"codex","expected_version":0,"items":[{"metric":"weekly","visible":true}]}]}`

	// When: stale version을 포함한 전체 payload를 저장한다.
	recorder := performMetricPreferenceRequest(server, nethttp.MethodPut, body)

	// Then: 전체 rollback 후 409와 두 Provider의 최신 canonical 설정을 반환하고 payload를 로깅하지 않는다.
	if recorder.Code != nethttp.StatusConflict {
		t.Fatalf("PUT status = %d, want 409; body=%s", recorder.Code, recorder.Body.String())
	}
	response := decodeMetricPreferenceResponse(t, recorder)
	if len(response.Providers) != 2 {
		t.Fatalf("conflict provider count = %d, want 2", len(response.Providers))
	}
	for _, expected := range []struct {
		name  string
		items []domain.MetricPreferenceItem
	}{
		{name: "claude", items: claudeItems},
		{name: "codex", items: codexItems},
	} {
		provider := findMetricPreferenceProvider(t, response.Providers, expected.name)
		if provider.Version != 1 || len(provider.Items) != len(expected.items) {
			t.Fatalf("latest canonical %s = %#v, want version 1 with %d items", expected.name, provider, len(expected.items))
		}
		for index, item := range provider.Items {
			if item.Metric != expected.items[index].Metric || item.Visible != expected.items[index].Visible || !item.Available {
				t.Fatalf("latest canonical %s item %d = %#v, want %#v available", expected.name, index, item, expected.items[index])
			}
		}
		storedProvider, err := server.store.GetProviderByName(expected.name)
		if err != nil {
			t.Fatalf("GetProviderByName(%q) error = %v", expected.name, err)
		}
		preference, err := server.store.GetMetricPreference(storedProvider.ID)
		if err != nil {
			t.Fatalf("GetMetricPreference(%q) error = %v", expected.name, err)
		}
		if preference.Version != 1 || !reflect.DeepEqual(preference.Items, expected.items) {
			t.Fatalf("stored %s changed after conflict: %#v", expected.name, preference)
		}
	}
	if strings.Contains(logs.String(), body) {
		t.Fatalf("logs contain full preference payload: %s", logs.String())
	}
}
