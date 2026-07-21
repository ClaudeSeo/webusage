package http

import (
	"strings"
	"testing"
)

func metricPreferenceFunctionSource(t *testing.T, body, name, nextName string) string {
	t.Helper()
	start := strings.Index(body, "function "+name+"(")
	if start < 0 {
		t.Fatalf("function %s missing from dashboard", name)
	}
	end := strings.Index(body[start+1:], "function "+nextName+"(")
	if end < 0 {
		t.Fatalf("function %s missing after %s", nextName, name)
	}
	return body[start : start+1+end]
}

func TestDashboardMetricPreferenceEditorShouldRenderAccessibleAvailableOnlyControls(t *testing.T) {
	// Given: metric preference editor를 포함하는 dashboard가 있다.
	server, _ := setupMetricPreferenceTestServer(t)

	// When: dashboard HTML을 조회한다.
	body := requestMetricPreferenceDashboard(t, server)

	// Then: editor의 접근성 markup과 available-only 조작 계약이 포함된다.
	for _, required := range []string{
		`id="metricPreferenceEditor"`,
		`id="metricPreferenceProviders"`,
		`id="metricPreferenceSaveButton"`,
		`id="metricPreferenceCancelButton"`,
		`id="metricPreferenceLatestButton"`,
		`id="metricPreferenceStatus"`,
		`role="status"`,
		`aria-live="polite"`,
		`설정할 항목 없음`,
		`metric-preference-drag-handle`,
		`row.draggable = true`,
		`toggle.type = 'checkbox'`,
		`textContent = item.label`,
		`textContent = item.metric`,
		`moveMetricPreference(provider.provider_id, item.metric, -1)`,
		`moveMetricPreference(provider.provider_id, item.metric, 1)`,
	} {
		if !strings.Contains(body, required) {
			t.Fatalf("dashboard missing metric editor contract %q", required)
		}
	}
	renderSource := metricPreferenceFunctionSource(t, body, "renderMetricPreferenceProviders", "createMetricPreferenceRow")
	if strings.Contains(renderSource, "innerHTML") {
		t.Fatal("metric provider renderer must not insert dynamic values with innerHTML")
	}
}

func TestDashboardMetricPreferenceEditorShouldPreserveDraftAndHandleSaveFailures(t *testing.T) {
	// Given: metric preference editor를 포함하는 dashboard가 있다.
	server, _ := setupMetricPreferenceTestServer(t)

	// When: dashboard HTML을 조회한다.
	body := requestMetricPreferenceDashboard(t, server)

	// Then: draft-only 편집, unavailable slot, 저장·취소·실패 처리 계약이 포함된다.
	for _, required := range []string{
		`fetch('/api/metric-preferences')`,
		`metricPreferenceSource`,
		`metricPreferenceDraft`,
		`availableIndexes`,
		`provider.items.map`,
		`metricPreferenceSaving`,
		`method: 'PUT'`,
		`response.status === 409`,
		`loadLatestMetricPreferences`,
		`최소 한 개의 사용량 항목은 표시해야 합니다`,
		`설정을 저장하지 못했습니다`,
		`네트워크 오류로 설정을 저장하지 못했습니다`,
	} {
		if !strings.Contains(body, required) {
			t.Fatalf("dashboard missing draft/save contract %q", required)
		}
	}
	saveSource := metricPreferenceFunctionSource(t, body, "saveMetricPreferences", "loadLatestMetricPreferences")
	if count := strings.Count(saveSource, "window.location.reload()"); count != 1 {
		t.Fatalf("save success reload count = %d, want exactly 1", count)
	}
}
