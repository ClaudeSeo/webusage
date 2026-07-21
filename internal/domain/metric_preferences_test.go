package domain

import (
	"reflect"
	"testing"
)

func TestMetricPreferenceShouldShowSortedCatalogWhenNoPreferencesAreSaved(t *testing.T) {
	// Given: 저장 설정 없이 순서가 섞인 최신 catalog가 있다.
	catalog := []string{"weekly", "session", "extra_credits"}

	// When: 노출 설정을 계산한다.
	got := ReconcileMetricPreferences(nil, catalog)

	// Then: 모든 metric이 key 오름차순으로 노출된다.
	want := []ReconciledMetricPreferenceItem{
		{Metric: "extra_credits", Label: "Extra 크레딧", Visible: true, Available: true},
		{Metric: "session", Label: "세션 (5h)", Visible: true, Available: true},
		{Metric: "weekly", Label: "주간 (7d)", Visible: true, Available: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReconcileMetricPreferences() = %#v, want %#v", got, want)
	}
}

func TestMetricPreferenceShouldRestoreSavedOrderAndVisibility(t *testing.T) {
	// Given: catalog와 다른 순서 및 hidden 상태가 저장되어 있다.
	saved := []MetricPreferenceItem{
		{Metric: "weekly", Visible: false},
		{Metric: "session", Visible: true},
	}
	catalog := []string{"session", "weekly"}

	// When: 노출 설정을 계산한다.
	got := ReconcileMetricPreferences(saved, catalog)

	// Then: 저장 순서와 visibility가 복원되고 label은 domain에서 계산된다.
	want := []ReconciledMetricPreferenceItem{
		{Metric: "weekly", Label: "주간 (7d)", Visible: false, Available: true},
		{Metric: "session", Label: "세션 (5h)", Visible: true, Available: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReconcileMetricPreferences() = %#v, want %#v", got, want)
	}
}

func TestMetricPreferenceShouldAppendNewMetricsSortedAndVisible(t *testing.T) {
	// Given: 저장 이후 두 metric이 catalog에 추가되었다.
	saved := []MetricPreferenceItem{{Metric: "weekly", Visible: false}}
	catalog := []string{"session", "weekly", "extra_credits"}

	// When: 노출 설정을 계산한다.
	got := ReconcileMetricPreferences(saved, catalog)

	// Then: 새 metric은 정렬된 상태로 저장 순서 뒤에 visible로 추가된다.
	want := []ReconciledMetricPreferenceItem{
		{Metric: "weekly", Label: "주간 (7d)", Visible: false, Available: true},
		{Metric: "extra_credits", Label: "Extra 크레딧", Visible: true, Available: true},
		{Metric: "session", Label: "세션 (5h)", Visible: true, Available: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReconcileMetricPreferences() = %#v, want %#v", got, want)
	}
}

func TestMetricPreferenceShouldPreserveUnavailableMetricAndRestoreItsPosition(t *testing.T) {
	// Given: 저장된 metric 하나가 현재 catalog에서 사라졌다.
	saved := []MetricPreferenceItem{
		{Metric: "weekly", Visible: false},
		{Metric: "session", Visible: true},
	}

	// When: metric이 사라진 catalog와 재등장한 catalog를 각각 계산한다.
	missing := ReconcileMetricPreferences(saved, []string{"session"})
	reappeared := ReconcileMetricPreferences(saved, []string{"session", "weekly"})

	// Then: unavailable 항목은 원래 위치와 visibility를 보존하고 재등장 시 같은 위치로 복귀한다.
	wantMissing := []ReconciledMetricPreferenceItem{
		{Metric: "weekly", Label: "주간 (7d)", Visible: false, Available: false},
		{Metric: "session", Label: "세션 (5h)", Visible: true, Available: true},
	}
	wantReappeared := []ReconciledMetricPreferenceItem{
		{Metric: "weekly", Label: "주간 (7d)", Visible: false, Available: true},
		{Metric: "session", Label: "세션 (5h)", Visible: true, Available: true},
	}
	if !reflect.DeepEqual(missing, wantMissing) {
		t.Fatalf("missing result = %#v, want %#v", missing, wantMissing)
	}
	if !reflect.DeepEqual(reappeared, wantReappeared) {
		t.Fatalf("reappeared result = %#v, want %#v", reappeared, wantReappeared)
	}
}

func TestMetricPreferenceShouldNotMutateInputs(t *testing.T) {
	// Given: 저장 설정과 catalog의 원본 복사본이 있다.
	saved := []MetricPreferenceItem{
		{Metric: "weekly", Visible: false},
		{Metric: "session", Visible: true},
	}
	catalog := []string{"weekly", "extra_credits", "session"}
	wantSaved := append([]MetricPreferenceItem(nil), saved...)
	wantCatalog := append([]string(nil), catalog...)

	// When: 노출 설정을 계산한다.
	_ = ReconcileMetricPreferences(saved, catalog)

	// Then: 호출자가 소유한 두 입력 slice는 변경되지 않는다.
	if !reflect.DeepEqual(saved, wantSaved) {
		t.Fatalf("saved input mutated: got %#v, want %#v", saved, wantSaved)
	}
	if !reflect.DeepEqual(catalog, wantCatalog) {
		t.Fatalf("catalog input mutated: got %#v, want %#v", catalog, wantCatalog)
	}
}
