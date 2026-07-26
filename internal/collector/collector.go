// Package collector manages usage data collection from OpenUsage API and
// native (webusage-internal) providers.
package collector

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ClaudeSeo/webusage/internal/native"
	"github.com/ClaudeSeo/webusage/internal/openusage"
	"github.com/ClaudeSeo/webusage/internal/store"
)

// Collector manages scheduled usage data collection from OpenUsage and native providers.
type Collector struct {
	store          *store.Store
	client         *openusage.Client // nil when OpenUsage is disabled
	nativeRegistry *native.Registry
	interval       time.Duration
	logger         *slog.Logger
	jobLocks       sync.Map
	jobStates      sync.Map // provider -> *JobState
}

// JobState tracks the state of a collection job
type JobState struct {
	running   atomic.Int32 // 0=idle, 1=running
	LastRun   time.Time
	LastError error
}

// NewCollector creates a new usage collector. client may be nil to disable the
// OpenUsage collection path (native providers still run).
func NewCollector(
	s *store.Store,
	client *openusage.Client,
	interval time.Duration,
	logger *slog.Logger,
) *Collector {
	return &Collector{
		store:    s,
		client:   client,
		interval: interval,
		logger:   logger,
	}
}

// SetNativeRegistry registers the native providers to collect from. Optional;
// when unset, the collector only uses OpenUsage (if a client is configured).
func (c *Collector) SetNativeRegistry(r *native.Registry) {
	c.nativeRegistry = r
}

// Start begins the collection loop
func (c *Collector) Start(ctx context.Context) error {
	c.logger.Info("Starting usage collector",
		"interval", c.interval.String(),
		"openusage_enabled", c.client != nil,
		"native_providers", c.nativeProviderCount())

	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	// Run immediately on start
	if err := c.collectAll(ctx); err != nil {
		c.logger.Error("Initial collection failed", "error", err)
	}

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("Stopping usage collector")
			return ctx.Err()
		case <-ticker.C:
			if err := c.collectAll(ctx); err != nil {
				c.logger.Error("Collection failed", "error", err)
			}
		}
	}
}

func (c *Collector) nativeProviderCount() int {
	if c.nativeRegistry == nil {
		return 0
	}
	return len(c.nativeRegistry.Providers())
}

// CollectAll triggers immediate collection from OpenUsage and native providers
func (c *Collector) CollectAll(ctx context.Context) error {
	return c.collectAll(ctx)
}

// collectAll fetches usage data from native providers (always) and from
// OpenUsage (when a client is configured). Native collection errors are logged
// but never abort the cycle; OpenUsage errors are returned to preserve the
// existing public contract.
func (c *Collector) collectAll(ctx context.Context) error {
	// Native providers run independently of OpenUsage so webusage can collect
	// on its own when OpenUsage is not running.
	c.collectNative(ctx)

	if c.client == nil {
		return nil
	}

	// Fetch all usage snapshots from OpenUsage
	snapshots, err := c.client.GetAllUsage()
	if err != nil {
		return fmt.Errorf("fetching from OpenUsage: %w", err)
	}

	c.logger.Info("Fetched usage data from OpenUsage", "providers", len(snapshots))

	var wg sync.WaitGroup
	for _, snapshot := range snapshots {
		wg.Add(1)
		go func(s openusage.UsageSnapshot) {
			defer wg.Done()
			c.processSnapshot(ctx, s)
		}(snapshot)
	}

	wg.Wait()
	return nil
}

// collectNative runs every available native provider sequentially. Native
// providers are cheap local reads, so sequential is fine and keeps locking
// simple.
func (c *Collector) collectNative(ctx context.Context) {
	if c.nativeRegistry == nil {
		return
	}
	for _, p := range c.nativeRegistry.Providers() {
		if !p.Available() {
			continue
		}
		metrics, err := p.Collect()
		if err != nil {
			c.logger.Warn("Native collection failed", "provider", p.Name(), "error", err)
			c.updateJobState(p.Name(), err)
			continue
		}
		snaps := metricsToSnapshots(metrics)
		c.persistSnapshots(p.Name(), snaps, time.Now())
	}
}

// processSnapshot converts an OpenUsage snapshot to store snapshots and saves them
func (c *Collector) processSnapshot(ctx context.Context, snapshot openusage.UsageSnapshot) {
	snaps := c.convertLinesToSnapshots(snapshot)
	c.persistSnapshots(snapshot.ProviderID, snaps, snapshot.FetchedAt)
}

// persistSnapshots acquires the provider lock, resolves or creates the DB
// provider row, stamps ProviderID and CollectedAt on each snapshot, stores
// them idempotently, and updates provider status + job state. Shared by the
// OpenUsage and native collection paths so both honor the same locking,
// idempotency, and status rules.
func (c *Collector) persistSnapshots(providerName string, snaps []*store.UsageSnapshot, fetchedAt time.Time) {
	// Try to lock this provider
	if !c.tryLock(providerName) {
		c.logger.Debug("Skipping collection - already running", "provider", providerName)
		return
	}
	defer c.unlock(providerName)

	// Look up the provider in the DB, creating it if missing. Distinguish error
	// kinds so a real DB error (not ErrNoRows) is not mistaken for "not found",
	// which would cause CreateProvider to hit a UNIQUE constraint and lose the
	// entire cycle's data.
	dbProvider, err := c.store.GetProviderByName(providerName)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			c.logger.Error("Failed to look up provider", "provider", providerName, "error", err)
			c.updateJobState(providerName, err)
			return
		}
		// Provider not in DB, create it
		providerID, err := c.store.CreateProvider(providerName, "")
		if err != nil {
			c.logger.Error("Failed to create provider", "provider", providerName, "error", err)
			c.updateJobState(providerName, err)
			return
		}
		dbProvider = &store.Provider{
			ID:      providerID,
			Name:    providerName,
			Enabled: true,
		}
	}

	// Store one canonical timezone so SQLite range comparisons work across providers.
	collectedAt := fetchedAt.UTC()
	for i := range snaps {
		snaps[i].ProviderID = dbProvider.ID
		snaps[i].CollectedAt = collectedAt
	}

	// Store in database
	if err := c.store.CreateUsageSnapshotsIdempotent(snaps); err != nil {
		c.logger.Error("Failed to store snapshots",
			"provider", providerName,
			"error", err)
		c.updateJobState(providerName, err)
		return
	}

	c.logger.Info("Collected usage data",
		"provider", providerName,
		"metrics", len(snaps),
		"fetchedAt", collectedAt)

	// Refresh providers.updated_at
	if err := c.store.UpdateProviderStatus(dbProvider.ID, nil); err != nil {
		c.logger.Warn("Failed to update provider status", "provider", providerName, "error", err)
	}

	c.updateJobState(providerName, nil)
}

// convertLinesToSnapshots converts OpenUsage lines to store.UsageSnapshot rows.
// ProviderID and CollectedAt are intentionally left zero — persistSnapshots
// stamps them so both collection paths stay consistent.
func (c *Collector) convertLinesToSnapshots(snapshot openusage.UsageSnapshot) []*store.UsageSnapshot {
	var snapshots []*store.UsageSnapshot

	for _, line := range snapshot.Lines {
		// Only process "progress" type lines (have numeric data)
		if line.Type != "progress" {
			continue
		}

		var resetAt *time.Time
		if line.ResetsAt != nil && *line.ResetsAt != "" {
			t, err := time.Parse(time.RFC3339, *line.ResetsAt)
			if err == nil {
				resetAt = &t
			}
		}

		var limit *float64
		if line.Limit > 0 {
			limit = &line.Limit
		}

		// Normalize metric name to lowercase for consistency
		// OpenUsage may return "Session" or "session" - we use lowercase
		metric := normalizeMetric(line.Label)

		snap := &store.UsageSnapshot{
			Metric:  metric,
			Used:    line.Used,
			Limit:   limit,
			ResetAt: resetAt,
		}

		snapshots = append(snapshots, snap)
	}

	return snapshots
}

// metricsToSnapshots converts native provider metrics to store.UsageSnapshot
// rows. ProviderID and CollectedAt are stamped by persistSnapshots.
func metricsToSnapshots(metrics []native.Metric) []*store.UsageSnapshot {
	if len(metrics) == 0 {
		return nil
	}
	snaps := make([]*store.UsageSnapshot, 0, len(metrics))
	for _, m := range metrics {
		snaps = append(snaps, &store.UsageSnapshot{
			Metric:  m.Metric,
			Used:    m.Used,
			Limit:   m.Limit,
			ResetAt: m.ResetAt,
			RawJSON: m.RawJSON,
		})
	}
	return snaps
}

// normalizeMetric converts metric labels to lowercase canonical form
func normalizeMetric(label string) string {
	// Common metric names - normalize to lowercase
	return strings.ToLower(label)
}

// tryLock attempts to acquire a lock for a provider
func (c *Collector) tryLock(providerName string) bool {
	state := &JobState{}
	loaded, _ := c.jobLocks.LoadOrStore(providerName, state)
	s := loaded.(*JobState)
	return s.running.CompareAndSwap(0, 1)
}

// unlock releases the lock for a provider
func (c *Collector) unlock(providerName string) {
	if state, ok := c.jobLocks.Load(providerName); ok {
		state.(*JobState).running.Store(0)
	}
}

// updateJobState updates the job state for a provider
func (c *Collector) updateJobState(providerName string, err error) {
	state := &JobState{LastRun: time.Now(), LastError: err}
	c.jobStates.Store(providerName, state)
}

// GetJobStates returns the current state of all collection jobs
func (c *Collector) GetJobStates() map[string]*JobState {
	states := make(map[string]*JobState)
	c.jobStates.Range(func(key, value interface{}) bool {
		states[key.(string)] = value.(*JobState)
		return true
	})
	return states
}
