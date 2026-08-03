package collector

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/ClaudeSeo/webusage/internal/native"
	"github.com/ClaudeSeo/webusage/internal/store"
)

// fakeProvider is a test double for the native.Provider interface. It verifies
// the native collection path at the behavior level without external dependencies
// (filesystem).
type fakeProvider struct {
	name      string
	available bool
	metrics   []native.Metric
	err       error
	lastCall  *fakeProviderCall
}

type fakeProviderCall struct {
	ctx context.Context
}

func (f *fakeProvider) Name() string    { return f.name }
func (f *fakeProvider) Available() bool { return f.available }
func (f *fakeProvider) Collect(ctx context.Context) ([]native.Metric, error) {
	f.lastCall = &fakeProviderCall{ctx: ctx}
	return f.metrics, f.err
}

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	path := t.TempDir() + "/collector_test.db"
	s, err := store.NewStore(path)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() {
		_ = s.Close()
	})
	return s
}

func newTestCollector(t *testing.T, s *store.Store, providers ...native.Provider) *Collector {
	t.Helper()
	c := NewCollector(s, nil, time.Minute, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	c.SetNativeRegistry(native.NewRegistry(providers...))
	return c
}

func TestCollectAllShouldPersistNativeMetricsWhenClientIsNil(t *testing.T) {
	// Given: no OpenUsage client, and one native provider returning 2 metrics.
	s := newTestStore(t)
	credits := 50.0
	resetAt := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	prov := &fakeProvider{
		name:      "kiro",
		available: true,
		metrics: []native.Metric{
			{Metric: "credits", Used: 12.5, Limit: &credits, ResetAt: &resetAt, RawJSON: `{"t":1}`},
			{Metric: "bonus_credits", Used: 1, Limit: nil, ResetAt: nil, RawJSON: ""},
		},
	}
	c := newTestCollector(t, s, prov)

	// When: CollectAll (native only, client=nil).
	if err := c.CollectAll(context.Background()); err != nil {
		t.Fatalf("CollectAll: %v", err)
	}

	// Then: a provider row is created and both metrics are stored.
	p, err := s.GetProviderByName("kiro")
	if err != nil {
		t.Fatalf("GetProviderByName: %v", err)
	}
	snaps, err := s.GetLatestUsageByProvider(p.ID)
	if err != nil {
		t.Fatalf("GetLatestUsageByProvider: %v", err)
	}
	if len(snaps) != 2 {
		t.Fatalf("snapshots = %d, want 2", len(snaps))
	}
	byMetric := map[string]*store.UsageSnapshot{}
	for _, sn := range snaps {
		byMetric[sn.Metric] = sn
	}
	if byMetric["credits"] == nil {
		t.Fatal("credits snapshot missing")
	}
	if byMetric["credits"].Used != 12.5 {
		t.Errorf("credits used = %v, want 12.5", byMetric["credits"].Used)
	}
	if byMetric["credits"].Limit == nil || *byMetric["credits"].Limit != 50 {
		t.Errorf("credits limit = %v, want 50", byMetric["credits"].Limit)
	}
	if byMetric["bonus_credits"] == nil {
		t.Fatal("bonus_credits snapshot missing")
	}
}

func TestPersistSnapshotsShouldKeepNativeUsageQueryableWhenFetchedAtHasLocalOffset(t *testing.T) {
	// Given: a native Kiro snapshot fetched in a non-UTC local timezone.
	s := newTestStore(t)
	c := newTestCollector(t, s)
	fetchedAt := time.Date(2026, 7, 27, 1, 54, 17, 0, time.FixedZone("KST", 9*60*60))
	c.persistSnapshots("kirocli", []*store.UsageSnapshot{{
		Metric: "credits",
		Used:   5427.92,
	}}, fetchedAt)

	p, err := s.GetProviderByName("kirocli")
	if err != nil {
		t.Fatalf("GetProviderByName: %v", err)
	}

	// When: the trend is queried with the equivalent UTC time window.
	snapshots, err := s.GetUsageTrends(
		p.ID,
		"credits",
		fetchedAt.UTC().Add(-time.Minute),
		fetchedAt.UTC().Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("GetUsageTrends: %v", err)
	}

	// Then: the native snapshot remains visible to the UTC-based chart query.
	if len(snapshots) != 1 {
		t.Fatalf("snapshots = %d, want 1", len(snapshots))
	}
	if !snapshots[0].CollectedAt.Equal(fetchedAt) {
		t.Errorf("CollectedAt = %v, want instant %v", snapshots[0].CollectedAt, fetchedAt)
	}
}

func TestCollectAllShouldSkipUnavailableNativeProviders(t *testing.T) {
	// Given: a provider with available=false.
	s := newTestStore(t)
	prov := &fakeProvider{name: "kiro", available: false, metrics: []native.Metric{{Metric: "credits", Used: 1}}}
	c := newTestCollector(t, s, prov)

	// When
	if err := c.CollectAll(context.Background()); err != nil {
		t.Fatalf("CollectAll: %v", err)
	}

	// Then: neither a provider row nor a snapshot is created.
	if _, err := s.GetProviderByName("kiro"); err == nil {
		t.Error("expected no provider row for unavailable provider")
	}
}

func TestCollectAllShouldPropagateServiceCancellationToNativeProviders(t *testing.T) {
	// Given: an available native provider and an already-cancelled collection context.
	s := newTestStore(t)
	prov := &fakeProvider{name: "kiro", available: true, metrics: []native.Metric{{Metric: "credits", Used: 1}}}
	c := newTestCollector(t, s, prov)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// When
	if err := c.CollectAll(ctx); err != nil {
		t.Fatalf("CollectAll: %v", err)
	}

	// Then: the provider observes the cancellation, so a provider doing external
	// I/O can abort its request instead of blocking shutdown.
	if prov.lastCall == nil {
		t.Fatal("Collect was not called")
	}
	if prov.lastCall.ctx == nil || prov.lastCall.ctx.Err() == nil {
		t.Error("Collect received a context that does not carry the service cancellation")
	}
}

func TestCollectAllShouldContinueWhenNativeProviderErrors(t *testing.T) {
	// Given: a provider whose Collect returns an error.
	s := newTestStore(t)
	prov := &fakeProvider{name: "broken", available: true, err: errFake("boom")}
	c := newTestCollector(t, s, prov)

	// When
	if err := c.CollectAll(context.Background()); err != nil {
		t.Fatalf("CollectAll: %v (native 에러는 사이클을 중단시키면 안 됨)", err)
	}

	// Then: completes without panic despite the error; no provider row/snapshot.
	if _, err := s.GetProviderByName("broken"); err == nil {
		t.Error("expected no provider row when Collect errors")
	}
}

func TestCollectAllShouldBeIdempotentWithinSameSecond(t *testing.T) {
	// Given: a provider returning the same metric.
	s := newTestStore(t)
	prov := &fakeProvider{name: "kiro", available: true, metrics: []native.Metric{{Metric: "credits", Used: 5}}}
	c := newTestCollector(t, s, prov)

	// When: collected twice within the same second.
	for i := 0; i < 2; i++ {
		if err := c.CollectAll(context.Background()); err != nil {
			t.Fatalf("CollectAll #%d: %v", i, err)
		}
	}

	// Then: the idempotent unique index ignores duplicates with the same
	// (provider, metric, second), so only one credits row exists.
	p, err := s.GetProviderByName("kiro")
	if err != nil {
		t.Fatalf("GetProviderByName: %v", err)
	}
	rows, err := s.DB().Query(`SELECT COUNT(*) FROM usage_snapshots WHERE provider_id = ? AND metric = ?`, p.ID, "credits")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	var n int
	if rows.Next() {
		_ = rows.Scan(&n)
	}
	if n != 1 {
		t.Errorf("credits rows = %d, want 1 (idempotent within same second)", n)
	}
}

func TestMetricsToSnapshotsShouldMapAllFieldsAndLeaveStampsZero(t *testing.T) {
	// Given: a native metric with all fields populated.
	limit := 50.0
	resetAt := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	metrics := []native.Metric{
		{Metric: "credits", Used: 12.5, Limit: &limit, ResetAt: &resetAt, RawJSON: `{"k":1}`},
	}

	// When
	snaps := metricsToSnapshots(metrics)

	// Then: fields are copied as-is, and ProviderID/CollectedAt are left zero
	// to be stamped during persist.
	if len(snaps) != 1 {
		t.Fatalf("snaps = %d, want 1", len(snaps))
	}
	sn := snaps[0]
	if sn.Metric != "credits" || sn.Used != 12.5 || sn.Limit == nil || *sn.Limit != 50 {
		t.Errorf("mapped fields = (%s, %v, %v), want (credits, 12.5, 50)", sn.Metric, sn.Used, sn.Limit)
	}
	if sn.ResetAt == nil || !sn.ResetAt.Equal(resetAt) {
		t.Errorf("resetAt = %v, want %v", sn.ResetAt, resetAt)
	}
	if sn.RawJSON != `{"k":1}` {
		t.Errorf("rawJSON = %q, want {\"k\":1}", sn.RawJSON)
	}
	if sn.ProviderID != 0 {
		t.Errorf("ProviderID = %d, want 0 (stamped later)", sn.ProviderID)
	}
	if !sn.CollectedAt.IsZero() {
		t.Errorf("CollectedAt = %v, want zero (stamped later)", sn.CollectedAt)
	}
}

func TestMetricsToSnapshotsShouldReturnNilForEmptyInput(t *testing.T) {
	// Given: an empty metric slice.
	// When
	snaps := metricsToSnapshots(nil)
	// Then: returns nil (not an empty slice) — treated as a no-op during persist.
	if snaps != nil {
		t.Errorf("snaps = %v, want nil for empty input", snaps)
	}
}

type errFake string

func (e errFake) Error() string { return string(e) }
