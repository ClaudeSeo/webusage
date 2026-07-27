package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func setupTestStore(t *testing.T) (*Store, func()) {
	// Create temp database
	tmpFile := "/tmp/test_usage_" + time.Now().Format("20060102150405") + ".db"

	store, err := NewStore(tmpFile)
	if err != nil {
		t.Fatalf("Failed to create test store: %v", err)
	}

	cleanup := func() {
		if err := store.Close(); err != nil {
			t.Errorf("Failed to close test store: %v", err)
		}
		for _, path := range []string{tmpFile, tmpFile + "-wal", tmpFile + "-shm"} {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				t.Errorf("Failed to remove test database file %q: %v", path, err)
			}
		}
	}

	return store, cleanup
}

func mustCreateStoreTestProvider(t *testing.T, testStore *Store) int64 {
	t.Helper()
	providerID, err := testStore.CreateProvider("test-provider", `{}`)
	if err != nil {
		t.Fatalf("Failed to create test provider: %v", err)
	}
	return providerID
}

func mustCreateStoreTestSnapshots(t *testing.T, testStore *Store, snapshots []*UsageSnapshot) {
	t.Helper()
	if err := testStore.CreateUsageSnapshots(snapshots); err != nil {
		t.Fatalf("Failed to create usage snapshots: %v", err)
	}
}

func TestNewStore_WALMode(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	// Verify WAL mode is enabled
	var journalMode string
	err := store.db.QueryRow("PRAGMA journal_mode").Scan(&journalMode)
	if err != nil {
		t.Fatalf("Failed to query journal mode: %v", err)
	}

	if journalMode != "wal" {
		t.Errorf("Expected WAL mode, got '%s'", journalMode)
	}
}

func TestNewStore_BusyTimeout(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	// Verify busy_timeout is set
	var timeout int
	err := store.db.QueryRow("PRAGMA busy_timeout").Scan(&timeout)
	if err != nil {
		t.Fatalf("Failed to query busy_timeout: %v", err)
	}

	if timeout != 5000 {
		t.Errorf("Expected busy_timeout=5000, got %d", timeout)
	}
}

func TestNewStoreShouldNormalizeLegacyCollectedAtWhenTimezonesAreMixed(t *testing.T) {
	// Given: an older KST row whose wall-clock text sorts after a newer UTC row.
	dbPath := filepath.Join(t.TempDir(), "usage.db")
	initialStore, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	providerID := mustCreateStoreTestProvider(t, initialStore)
	kst := time.FixedZone("KST", 9*60*60)
	legacyTime := time.Date(2026, 7, 27, 1, 58, 15, 0, kst)
	newerTime := time.Date(2026, 7, 27, 0, 7, 43, 0, time.UTC)
	for _, snapshot := range []*UsageSnapshot{
		{ProviderID: providerID, Metric: "credits", Used: 100, CollectedAt: legacyTime},
		{ProviderID: providerID, Metric: "credits", Used: 200, CollectedAt: newerTime},
	} {
		_, err := initialStore.db.Exec(`
			INSERT INTO usage_snapshots
			(provider_id, metric, used, collected_at, raw_json)
			VALUES (?, ?, ?, ?, '')
		`, snapshot.ProviderID, snapshot.Metric, snapshot.Used, snapshot.CollectedAt)
		if err != nil {
			t.Fatalf("inserting legacy snapshot error = %v", err)
		}
	}
	if err := initialStore.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// When: the existing database is reopened.
	reopenedStore, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer func() {
		if err := reopenedStore.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()
	latest, err := reopenedStore.GetLatestUsage(providerID, "credits")
	if err != nil {
		t.Fatalf("GetLatestUsage() error = %v", err)
	}

	// Then: chronological order wins and all stored collection times are UTC.
	if latest == nil {
		t.Fatal("GetLatestUsage() = nil, want latest snapshot")
	}
	if latest.Used != 200 {
		t.Errorf("latest Used = %v, want 200", latest.Used)
	}
	rows, err := reopenedStore.db.Query(`SELECT collected_at FROM usage_snapshots`)
	if err != nil {
		t.Fatalf("querying collected_at error = %v", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var collectedAt time.Time
		if err := rows.Scan(&collectedAt); err != nil {
			t.Fatalf("scanning collected_at error = %v", err)
		}
		if collectedAt.Location() != time.UTC {
			t.Errorf("collected_at location = %v, want UTC", collectedAt.Location())
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterating collected_at rows error = %v", err)
	}
}

func TestStore_ProviderCRUD(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	// Create provider
	id, err := store.CreateProvider("test-provider", `{"auth_method":"oauth_file"}`)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	if id == 0 {
		t.Error("Expected non-zero provider ID")
	}

	// Get by ID
	p, err := store.GetProvider(id)
	if err != nil {
		t.Fatalf("Failed to get provider: %v", err)
	}

	if p.Name != "test-provider" {
		t.Errorf("Expected name 'test-provider', got '%s'", p.Name)
	}

	if !p.Enabled {
		t.Error("Expected provider to be enabled by default")
	}

	// Get by name
	p2, err := store.GetProviderByName("test-provider")
	if err != nil {
		t.Fatalf("Failed to get provider by name: %v", err)
	}

	if p2.ID != id {
		t.Errorf("Expected ID %d, got %d", id, p2.ID)
	}

	// List providers
	providers, err := store.ListProviders()
	if err != nil {
		t.Fatalf("Failed to list providers: %v", err)
	}

	if len(providers) != 1 {
		t.Errorf("Expected 1 provider, got %d", len(providers))
	}

	// Update status
	err = store.UpdateProviderStatus(id, nil)
	if err != nil {
		t.Fatalf("Failed to update provider status: %v", err)
	}

	// Verify update
	p3, err := store.GetProvider(id)
	if err != nil {
		t.Fatalf("Failed to get updated provider: %v", err)
	}
	if p3.LastRun == nil {
		t.Error("Expected LastRun to be set")
	}

	// Disable provider
	err = store.EnableProvider(id, false)
	if err != nil {
		t.Fatalf("Failed to disable provider: %v", err)
	}

	p4, err := store.GetProvider(id)
	if err != nil {
		t.Fatalf("Failed to get disabled provider: %v", err)
	}
	if p4.Enabled {
		t.Error("Expected provider to be disabled")
	}

	// Delete provider
	err = store.DeleteProvider(id)
	if err != nil {
		t.Fatalf("Failed to delete provider: %v", err)
	}

	providers, err = store.ListProviders()
	if err != nil {
		t.Fatalf("Failed to list providers after deletion: %v", err)
	}
	if len(providers) != 0 {
		t.Errorf("Expected 0 providers after deletion, got %d", len(providers))
	}
}

func TestStore_UsageSnapshotCRUD(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	// Create provider
	providerID := mustCreateStoreTestProvider(t, store)

	now := time.Now()
	limit := 100000.0

	// Create single snapshot
	snapshot := &UsageSnapshot{
		ProviderID:  providerID,
		Metric:      "tokens",
		Used:        5000.0,
		Limit:       &limit,
		ResetAt:     &now,
		CollectedAt: now,
		RawJSON:     `{"test":true}`,
	}

	id, err := store.CreateUsageSnapshot(snapshot)
	if err != nil {
		t.Fatalf("Failed to create snapshot: %v", err)
	}

	if id == 0 {
		t.Error("Expected non-zero snapshot ID")
	}

	// Get latest usage
	latest, err := store.GetLatestUsage(providerID, "tokens")
	if err != nil {
		t.Fatalf("Failed to get latest usage: %v", err)
	}

	if latest == nil {
		t.Fatal("Expected non-nil latest usage")
	}

	if latest.Used != 5000.0 {
		t.Errorf("Expected used 5000.0, got %f", latest.Used)
	}

	if latest.Limit == nil || *latest.Limit != limit {
		t.Errorf("Expected limit %f, got %v", limit, latest.Limit)
	}
}

func TestStore_CreateUsageSnapshots_Batch(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	providerID := mustCreateStoreTestProvider(t, store)

	now := time.Now()
	snapshots := []*UsageSnapshot{
		{ProviderID: providerID, Metric: "tokens", Used: 1000, CollectedAt: now},
		{ProviderID: providerID, Metric: "requests", Used: 10, CollectedAt: now},
		{ProviderID: providerID, Metric: "errors", Used: 0, CollectedAt: now},
	}

	err := store.CreateUsageSnapshots(snapshots)
	if err != nil {
		t.Fatalf("Failed to create batch snapshots: %v", err)
	}

	// Verify all were created
	latest, err := store.GetLatestUsageByProvider(providerID)
	if err != nil {
		t.Fatalf("Failed to get latest usage: %v", err)
	}
	if len(latest) != 3 {
		t.Errorf("Expected 3 snapshots, got %d", len(latest))
	}
}

func TestStore_GetUsageTrends(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	providerID := mustCreateStoreTestProvider(t, store)

	now := time.Now()
	startTime := now.Add(-2 * time.Hour)

	// Insert historical data
	snapshots := []*UsageSnapshot{
		{ProviderID: providerID, Metric: "tokens", Used: 1000, CollectedAt: startTime},
		{ProviderID: providerID, Metric: "tokens", Used: 2000, CollectedAt: startTime.Add(time.Hour)},
		{ProviderID: providerID, Metric: "tokens", Used: 3000, CollectedAt: now},
	}

	mustCreateStoreTestSnapshots(t, store, snapshots)

	// Get trends
	trends, err := store.GetUsageTrends(providerID, "tokens", startTime, now)
	if err != nil {
		t.Fatalf("Failed to get trends: %v", err)
	}

	if len(trends) != 3 {
		t.Errorf("Expected 3 trend points, got %d", len(trends))
	}

	// Verify order (ascending)
	if trends[0].Used > trends[2].Used {
		t.Error("Expected trends to be ordered by time ascending")
	}
}

func TestStore_GetAggregatedUsage(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	providerID := mustCreateStoreTestProvider(t, store)

	now := time.Now()
	startTime := now.Add(-time.Hour)

	// Insert data
	snapshots := []*UsageSnapshot{
		{ProviderID: providerID, Metric: "tokens", Used: 1000, CollectedAt: startTime},
		{ProviderID: providerID, Metric: "tokens", Used: 2000, CollectedAt: now},
	}

	mustCreateStoreTestSnapshots(t, store, snapshots)

	// Get aggregated
	total, err := store.GetAggregatedUsage(providerID, "tokens", startTime, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Failed to get aggregated usage: %v", err)
	}

	expected := 3000.0
	if total != expected {
		t.Errorf("Expected total %f, got %f", expected, total)
	}
}

func TestStore_DeleteOldUsage(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	providerID := mustCreateStoreTestProvider(t, store)

	oldTime := time.Now().Add(-24 * time.Hour)
	newTime := time.Now()

	// Insert old and new data
	snapshots := []*UsageSnapshot{
		{ProviderID: providerID, Metric: "tokens", Used: 1000, CollectedAt: oldTime},
		{ProviderID: providerID, Metric: "tokens", Used: 2000, CollectedAt: newTime},
	}

	mustCreateStoreTestSnapshots(t, store, snapshots)

	// Delete old data
	cutoff := time.Now().Add(-12 * time.Hour)
	deleted, err := store.DeleteOldUsage(cutoff)
	if err != nil {
		t.Fatalf("Failed to delete old usage: %v", err)
	}

	if deleted != 1 {
		t.Errorf("Expected to delete 1 record, got %d", deleted)
	}

	// Verify only new data remains
	remaining, err := store.GetLatestUsage(providerID, "tokens")
	if err != nil {
		t.Fatalf("Failed to get remaining usage: %v", err)
	}
	if remaining == nil || remaining.Used != 2000 {
		t.Error("Expected only new data to remain")
	}
}
