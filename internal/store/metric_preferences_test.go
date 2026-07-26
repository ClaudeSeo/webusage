package store

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/ClaudeSeo/webusage/internal/domain"
)

func TestMetricPreferenceShouldReturnVersionZeroWhenNoRowExists(t *testing.T) {
	// Given: a Provider with no saved preference exists.
	store, cleanup := setupTestStore(t)
	defer cleanup()
	providerID, err := store.CreateProvider("metric-pref-empty", `{"auth_method":"oauth_file"}`)
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}

	// When: the preference is fetched.
	preference, err := store.GetMetricPreference(providerID)

	// Then: returns version 0 and an empty item list.
	if err != nil {
		t.Fatalf("GetMetricPreference() error = %v", err)
	}
	if preference.ProviderID != providerID || preference.Version != 0 {
		t.Fatalf("GetMetricPreference() = %#v, want provider %d version 0", preference, providerID)
	}
	if preference.Items == nil || len(preference.Items) != 0 {
		t.Fatalf("Items = %#v, want non-nil empty slice", preference.Items)
	}
}

func TestMetricPreferenceShouldInsertVersionOneWithoutChangingProviderConfig(t *testing.T) {
	// Given: a Provider with auth config and an initial preference save request.
	store, cleanup := setupTestStore(t)
	defer cleanup()
	configJSON := `{"auth_method":"oauth_file","cred_source":"keychain"}`
	providerID, err := store.CreateProvider("metric-pref-insert", configJSON)
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}
	items := []domain.MetricPreferenceItem{
		{Metric: "weekly", Visible: false},
		{Metric: "session", Visible: true},
	}

	// When: saved with expected version 0.
	err = store.SaveMetricPreferences([]MetricPreferenceUpdate{{
		ProviderID:      providerID,
		ExpectedVersion: 0,
		Items:           items,
	}})

	// Then: saved as version 1 and providers.config_json stays unchanged.
	if err != nil {
		t.Fatalf("SaveMetricPreferences() error = %v", err)
	}
	preference, err := store.GetMetricPreference(providerID)
	if err != nil {
		t.Fatalf("GetMetricPreference() error = %v", err)
	}
	if preference.Version != 1 || !reflect.DeepEqual(preference.Items, items) {
		t.Fatalf("preference = %#v, want version 1 items %#v", preference, items)
	}
	provider, err := store.GetProvider(providerID)
	if err != nil {
		t.Fatalf("GetProvider() error = %v", err)
	}
	if provider.ConfigJSON != configJSON {
		t.Fatalf("ConfigJSON = %q, want %q", provider.ConfigJSON, configJSON)
	}
}

func TestMetricPreferenceShouldIncrementVersionExactlyOnceOnUpdate(t *testing.T) {
	// Given: a version 1 preference is already saved.
	store, cleanup := setupTestStore(t)
	defer cleanup()
	providerID, err := store.CreateProvider("metric-pref-update", `{}`)
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}
	if err := store.SaveMetricPreferences([]MetricPreferenceUpdate{{
		ProviderID: providerID,
		Items:      []domain.MetricPreferenceItem{{Metric: "session", Visible: true}},
	}}); err != nil {
		t.Fatalf("initial SaveMetricPreferences() error = %v", err)
	}
	updatedItems := []domain.MetricPreferenceItem{{Metric: "session", Visible: false}}

	// When: updated once with expected version 1.
	err = store.SaveMetricPreferences([]MetricPreferenceUpdate{{
		ProviderID:      providerID,
		ExpectedVersion: 1,
		Items:           updatedItems,
	}})

	// Then: item and version change to the request value and 2 respectively.
	if err != nil {
		t.Fatalf("SaveMetricPreferences() error = %v", err)
	}
	preference, err := store.GetMetricPreference(providerID)
	if err != nil {
		t.Fatalf("GetMetricPreference() error = %v", err)
	}
	if preference.Version != 2 || !reflect.DeepEqual(preference.Items, updatedItems) {
		t.Fatalf("preference = %#v, want version 2 items %#v", preference, updatedItems)
	}
}

func TestMetricPreferenceShouldRollbackAllProvidersWhenOneVersionConflicts(t *testing.T) {
	// Given: both Providers are at version 1, but only the second update submits a stale version.
	store, cleanup := setupTestStore(t)
	defer cleanup()
	firstID, err := store.CreateProvider("metric-pref-atomic-first", `{}`)
	if err != nil {
		t.Fatalf("CreateProvider(first) error = %v", err)
	}
	secondID, err := store.CreateProvider("metric-pref-atomic-second", `{}`)
	if err != nil {
		t.Fatalf("CreateProvider(second) error = %v", err)
	}
	initialFirst := []domain.MetricPreferenceItem{{Metric: "session", Visible: true}}
	initialSecond := []domain.MetricPreferenceItem{{Metric: "weekly", Visible: true}}
	if err := store.SaveMetricPreferences([]MetricPreferenceUpdate{
		{ProviderID: firstID, Items: initialFirst},
		{ProviderID: secondID, Items: initialSecond},
	}); err != nil {
		t.Fatalf("initial SaveMetricPreferences() error = %v", err)
	}

	// When: the first Provider updates with a valid version and the second with a stale version, together.
	err = store.SaveMetricPreferences([]MetricPreferenceUpdate{
		{ProviderID: firstID, ExpectedVersion: 1, Items: []domain.MetricPreferenceItem{{Metric: "session", Visible: false}}},
		{ProviderID: secondID, ExpectedVersion: 0, Items: []domain.MetricPreferenceItem{{Metric: "weekly", Visible: false}}},
	})

	// Then: a version conflict is returned and the first Provider's change is also rolled back.
	if !errors.Is(err, ErrMetricPreferenceVersionConflict) {
		t.Fatalf("SaveMetricPreferences() error = %v, want ErrMetricPreferenceVersionConflict", err)
	}
	first, err := store.GetMetricPreference(firstID)
	if err != nil {
		t.Fatalf("GetMetricPreference(first) error = %v", err)
	}
	second, err := store.GetMetricPreference(secondID)
	if err != nil {
		t.Fatalf("GetMetricPreference(second) error = %v", err)
	}
	if first.Version != 1 || !reflect.DeepEqual(first.Items, initialFirst) {
		t.Fatalf("first preference changed after rollback: %#v", first)
	}
	if second.Version != 1 || !reflect.DeepEqual(second.Items, initialSecond) {
		t.Fatalf("second preference changed after rollback: %#v", second)
	}
}

func TestMetricPreferenceShouldReturnErrorForMalformedJSON(t *testing.T) {
	// Given: an invalid preference JSON row exists in the DB.
	store, cleanup := setupTestStore(t)
	defer cleanup()
	providerID, err := store.CreateProvider("metric-pref-malformed", `{}`)
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}
	_, err = store.db.Exec(`
		INSERT INTO provider_metric_preferences (provider_id, items_json, version)
		VALUES (?, ?, 1)
	`, providerID, `[{"metric":`)
	if err != nil {
		t.Fatalf("insert malformed preference error = %v", err)
	}

	// When: the preference is fetched.
	_, err = store.GetMetricPreference(providerID)

	// Then: the malformed JSON error is not hidden.
	if err == nil {
		t.Fatal("GetMetricPreference() error = nil, want malformed JSON error")
	}
}

func TestMetricPreferenceShouldCascadeDeleteWhenProviderIsDeleted(t *testing.T) {
	// Given: a Provider with a saved preference exists.
	store, cleanup := setupTestStore(t)
	defer cleanup()
	providerID, err := store.CreateProvider("metric-pref-cascade", `{}`)
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}
	if err := store.SaveMetricPreferences([]MetricPreferenceUpdate{{
		ProviderID: providerID,
		Items:      []domain.MetricPreferenceItem{{Metric: "session", Visible: true}},
	}}); err != nil {
		t.Fatalf("SaveMetricPreferences() error = %v", err)
	}

	// When: the Provider is deleted.
	if err := store.DeleteProvider(providerID); err != nil {
		t.Fatalf("DeleteProvider() error = %v", err)
	}

	// Then: the linked preference row is also cascade-deleted.
	var count int
	if err := store.db.QueryRow(`
		SELECT COUNT(*) FROM provider_metric_preferences WHERE provider_id = ?
	`, providerID).Scan(&count); err != nil {
		t.Fatalf("count preference rows error = %v", err)
	}
	if count != 0 {
		t.Fatalf("preference row count = %d, want 0", count)
	}
}

func TestMetricPreferenceShouldRejectNullUpdatedAt(t *testing.T) {
	// Given: a Provider to link the preference to exists.
	store, cleanup := setupTestStore(t)
	defer cleanup()
	providerID, err := store.CreateProvider("metric-pref-updated-at", `{}`)
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}

	// When: a preference row is inserted with updated_at set to NULL.
	_, err = store.db.Exec(`
		INSERT INTO provider_metric_preferences (provider_id, items_json, version, updated_at)
		VALUES (?, '[]', 1, NULL)
	`, providerID)

	// Then: the schema rejects a NULL timestamp.
	if err == nil {
		t.Fatal("inserting NULL updated_at error = nil, want NOT NULL constraint error")
	}
}

func TestMetricPreferenceShouldAllowExactlyOneConcurrentCASUpdate(t *testing.T) {
	// Given: two changes use the same expected version as the version 1 setting.
	store, cleanup := setupTestStore(t)
	defer cleanup()
	providerID, err := store.CreateProvider("metric-pref-concurrent", `{}`)
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}
	if err := store.SaveMetricPreferences([]MetricPreferenceUpdate{{
		ProviderID: providerID,
		Items:      []domain.MetricPreferenceItem{{Metric: "session", Visible: true}},
	}}); err != nil {
		t.Fatalf("initial SaveMetricPreferences() error = %v", err)
	}
	candidates := [][]domain.MetricPreferenceItem{
		{{Metric: "session", Visible: false}},
		{{Metric: "weekly", Visible: true}},
	}
	start := make(chan struct{})
	errorsByUpdate := make(chan error, len(candidates))
	var ready sync.WaitGroup
	ready.Add(len(candidates))

	// When: two goroutines start CAS updates with the same version concurrently.
	for _, items := range candidates {
		items := items
		go func() {
			ready.Done()
			<-start
			errorsByUpdate <- store.SaveMetricPreferences([]MetricPreferenceUpdate{{
				ProviderID:      providerID,
				ExpectedVersion: 1,
				Items:           items,
			}})
		}()
	}
	ready.Wait()
	close(start)

	// Then: exactly one succeeds and the final setting is one of the successful candidates with version 2.
	successes := 0
	conflicts := 0
	for range candidates {
		err := <-errorsByUpdate
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrMetricPreferenceVersionConflict):
			conflicts++
		default:
			t.Fatalf("concurrent SaveMetricPreferences() unexpected error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent results = %d success, %d conflicts; want 1 and 1", successes, conflicts)
	}
	preference, err := store.GetMetricPreference(providerID)
	if err != nil {
		t.Fatalf("GetMetricPreference() error = %v", err)
	}
	if preference.Version != 2 {
		t.Fatalf("final version = %d, want 2", preference.Version)
	}
	if !reflect.DeepEqual(preference.Items, candidates[0]) && !reflect.DeepEqual(preference.Items, candidates[1]) {
		t.Fatalf("final items = %#v, want one concurrent candidate", preference.Items)
	}
}
