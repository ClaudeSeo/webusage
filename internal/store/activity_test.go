package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSnapshotActivityCoverageCountsSnapshotsAndUniqueCollections(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "usage.db"))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()
	providerID, err := store.CreateProvider("activity", `{}`)
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}
	loc, err := time.LoadLocation("Asia/Seoul")
	if err != nil {
		t.Fatal(err)
	}
	end := time.Date(2026, 8, 5, 0, 0, 0, 0, loc)
	start := end.AddDate(0, 0, -13)
	collection := time.Date(2026, 8, 4, 1, 5, 0, 0, loc).UTC()
	if err := store.CreateUsageSnapshots([]*UsageSnapshot{
		{ProviderID: providerID, Metric: "session", Used: 1, CollectedAt: collection},
		{ProviderID: providerID, Metric: "weekly", Used: 2, CollectedAt: collection},
		{ProviderID: providerID, Metric: "session", Used: 3, CollectedAt: collection.Add(2 * time.Hour)},
	}); err != nil {
		t.Fatalf("CreateUsageSnapshots() error = %v", err)
	}

	coverage, err := store.GetSnapshotActivityCoverage(start, end.AddDate(0, 0, 1), loc)
	if err != nil {
		t.Fatalf("GetSnapshotActivityCoverage() error = %v", err)
	}
	if len(coverage.Cells) != 14*24 {
		t.Fatalf("cells = %d, want 336", len(coverage.Cells))
	}
	if coverage.TotalSnapshots != 3 {
		t.Fatalf("total snapshots = %d, want 3", coverage.TotalSnapshots)
	}
	if coverage.UniqueCollections != 2 {
		t.Fatalf("unique collections = %d, want 2", coverage.UniqueCollections)
	}
	if coverage.Cell("2026-08-04", 1).SnapshotCount != 2 {
		t.Fatalf("same-hour cell count = %d, want 2", coverage.Cell("2026-08-04", 1).SnapshotCount)
	}
}
