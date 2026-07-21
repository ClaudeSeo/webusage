package store

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/ClaudeSeo/webusage/internal/domain"
)

func TestMetricPreferenceShouldReturnVersionZeroWhenNoRowExists(t *testing.T) {
	// Given: preference를 저장하지 않은 Provider가 있다.
	store, cleanup := setupTestStore(t)
	defer cleanup()
	providerID, err := store.CreateProvider("metric-pref-empty", `{"auth_method":"oauth_file"}`)
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}

	// When: preference를 조회한다.
	preference, err := store.GetMetricPreference(providerID)

	// Then: version 0과 비어 있는 item 목록을 반환한다.
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
	// Given: 인증 설정이 있는 Provider와 최초 preference 저장 요청이 있다.
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

	// When: expected version 0으로 저장한다.
	err = store.SaveMetricPreferences([]MetricPreferenceUpdate{{
		ProviderID:      providerID,
		ExpectedVersion: 0,
		Items:           items,
	}})

	// Then: version 1로 저장되고 providers.config_json은 그대로다.
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
	// Given: version 1 preference가 저장되어 있다.
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

	// When: expected version 1로 한 번 갱신한다.
	err = store.SaveMetricPreferences([]MetricPreferenceUpdate{{
		ProviderID:      providerID,
		ExpectedVersion: 1,
		Items:           updatedItems,
	}})

	// Then: item과 version이 각각 요청값과 2로 바뀐다.
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
	// Given: 두 Provider가 모두 version 1이며 두 번째 갱신만 stale version을 제출한다.
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

	// When: 첫 Provider는 정상 version, 두 번째는 stale version으로 함께 갱신한다.
	err = store.SaveMetricPreferences([]MetricPreferenceUpdate{
		{ProviderID: firstID, ExpectedVersion: 1, Items: []domain.MetricPreferenceItem{{Metric: "session", Visible: false}}},
		{ProviderID: secondID, ExpectedVersion: 0, Items: []domain.MetricPreferenceItem{{Metric: "weekly", Visible: false}}},
	})

	// Then: version conflict가 반환되고 첫 Provider 변경까지 rollback된다.
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
	// Given: DB에 유효하지 않은 preference JSON 행이 있다.
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

	// When: preference를 조회한다.
	_, err = store.GetMetricPreference(providerID)

	// Then: malformed JSON 오류를 숨기지 않는다.
	if err == nil {
		t.Fatal("GetMetricPreference() error = nil, want malformed JSON error")
	}
}

func TestMetricPreferenceShouldCascadeDeleteWhenProviderIsDeleted(t *testing.T) {
	// Given: preference가 저장된 Provider가 있다.
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

	// When: Provider를 삭제한다.
	if err := store.DeleteProvider(providerID); err != nil {
		t.Fatalf("DeleteProvider() error = %v", err)
	}

	// Then: 연결된 preference 행도 cascade 삭제된다.
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
	// Given: preference를 연결할 Provider가 있다.
	store, cleanup := setupTestStore(t)
	defer cleanup()
	providerID, err := store.CreateProvider("metric-pref-updated-at", `{}`)
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}

	// When: updated_at을 NULL로 지정해 preference 행을 삽입한다.
	_, err = store.db.Exec(`
		INSERT INTO provider_metric_preferences (provider_id, items_json, version, updated_at)
		VALUES (?, '[]', 1, NULL)
	`, providerID)

	// Then: schema가 NULL timestamp를 거부한다.
	if err == nil {
		t.Fatal("inserting NULL updated_at error = nil, want NOT NULL constraint error")
	}
}

func TestMetricPreferenceShouldAllowExactlyOneConcurrentCASUpdate(t *testing.T) {
	// Given: version 1 설정과 같은 expected version을 쓰는 두 변경이 있다.
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

	// When: 두 goroutine이 같은 version으로 CAS 갱신을 동시에 시작한다.
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

	// Then: 정확히 하나만 성공하고 최종 설정은 성공 후보 중 하나와 version 2다.
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
