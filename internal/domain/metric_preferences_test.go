package domain

import (
	"reflect"
	"testing"
)

func TestMetricPreferenceShouldShowSortedCatalogWhenNoPreferencesAreSaved(t *testing.T) {
	// Given: a latest catalog with shuffled order and no saved preferences.
	catalog := []string{"weekly", "session", "extra_credits"}

	// When: compute the display preferences.
	got := ReconcileMetricPreferences(nil, catalog)

	// Then: every metric is shown in ascending key order.
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
	// Given: a saved order and hidden state different from the catalog.
	saved := []MetricPreferenceItem{
		{Metric: "weekly", Visible: false},
		{Metric: "session", Visible: true},
	}
	catalog := []string{"session", "weekly"}

	// When: compute the display preferences.
	got := ReconcileMetricPreferences(saved, catalog)

	// Then: saved order and visibility are restored, and the label is computed by the domain.
	want := []ReconciledMetricPreferenceItem{
		{Metric: "weekly", Label: "주간 (7d)", Visible: false, Available: true},
		{Metric: "session", Label: "세션 (5h)", Visible: true, Available: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReconcileMetricPreferences() = %#v, want %#v", got, want)
	}
}

func TestMetricPreferenceShouldAppendNewMetricsSortedAndVisible(t *testing.T) {
	// Given: two metrics were added to the catalog after saving.
	saved := []MetricPreferenceItem{{Metric: "weekly", Visible: false}}
	catalog := []string{"session", "weekly", "extra_credits"}

	// When: compute the display preferences.
	got := ReconcileMetricPreferences(saved, catalog)

	// Then: new metrics are appended after the saved order, sorted and visible.
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
	// Given: one saved metric is missing from the current catalog.
	saved := []MetricPreferenceItem{
		{Metric: "weekly", Visible: false},
		{Metric: "session", Visible: true},
	}

	// When: compute against a catalog where the metric is missing, and one where it reappears.
	missing := ReconcileMetricPreferences(saved, []string{"session"})
	reappeared := ReconcileMetricPreferences(saved, []string{"session", "weekly"})

	// Then: unavailable items preserve their original position and visibility, and return to the same position when they reappear.
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
	// Given: original copies of saved preferences and the catalog.
	saved := []MetricPreferenceItem{
		{Metric: "weekly", Visible: false},
		{Metric: "session", Visible: true},
	}
	catalog := []string{"weekly", "extra_credits", "session"}
	wantSaved := append([]MetricPreferenceItem(nil), saved...)
	wantCatalog := append([]string(nil), catalog...)

	// When: compute the display preferences.
	_ = ReconcileMetricPreferences(saved, catalog)

	// Then: both caller-owned input slices remain unchanged.
	if !reflect.DeepEqual(saved, wantSaved) {
		t.Fatalf("saved input mutated: got %#v, want %#v", saved, wantSaved)
	}
	if !reflect.DeepEqual(catalog, wantCatalog) {
		t.Fatalf("catalog input mutated: got %#v, want %#v", catalog, wantCatalog)
	}
}
