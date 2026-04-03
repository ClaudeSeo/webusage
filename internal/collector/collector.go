// Package collector manages usage data collection from OpenUsage API.
package collector

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ClaudeSeo/webusage/internal/openusage"
	"github.com/ClaudeSeo/webusage/internal/store"
)

// Collector manages scheduled usage data collection from OpenUsage
type Collector struct {
	store     *store.Store
	client    *openusage.Client
	interval  time.Duration
	logger    *slog.Logger
	jobLocks  sync.Map
	jobStates sync.Map // provider -> *JobState
}

// JobState tracks the state of a collection job
type JobState struct {
	running   atomic.Int32 // 0=idle, 1=running
	LastRun   time.Time
	LastError error
}

// NewCollector creates a new usage collector
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

// Start begins the collection loop
func (c *Collector) Start(ctx context.Context) error {
	c.logger.Info("Starting usage collector from OpenUsage API",
		"interval", c.interval.String())

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

// CollectAll triggers immediate collection from OpenUsage
func (c *Collector) CollectAll(ctx context.Context) error {
	return c.collectAll(ctx)
}

// collectAll fetches all usage data from OpenUsage and stores it
func (c *Collector) collectAll(ctx context.Context) error {
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

// processSnapshot converts an OpenUsage snapshot to store snapshots and saves them
func (c *Collector) processSnapshot(ctx context.Context, snapshot openusage.UsageSnapshot) {
	providerName := snapshot.ProviderID

	// Try to lock this provider
	if !c.tryLock(providerName) {
		c.logger.Debug("Skipping collection - already running", "provider", providerName)
		return
	}
	defer c.unlock(providerName)

	// Get or create provider in DB
	dbProvider, err := c.store.GetProviderByName(providerName)
	if err != nil {
		// Provider not in DB, create it
		providerID, err := c.store.CreateProvider(providerName, "")
		if err != nil {
			c.logger.Error("Failed to create provider", "provider", providerName, "error", err)
			return
		}
		// Enable it by default since OpenUsage is providing data
		c.store.EnableProviderByName(providerName, true)
		dbProvider = &store.Provider{
			ID:      providerID,
			Name:    providerName,
			Enabled: true,
		}
	}

	// Convert lines to usage snapshots
	storeSnapshots := c.convertLinesToSnapshots(dbProvider.ID, snapshot)

	// Store in database
	if err := c.store.CreateUsageSnapshotsIdempotent(storeSnapshots); err != nil {
		c.logger.Error("Failed to store snapshots",
			"provider", providerName,
			"error", err)
		c.updateJobState(providerName, err)
		return
	}

	c.logger.Info("Collected usage data",
		"provider", providerName,
		"metrics", len(storeSnapshots),
		"fetchedAt", snapshot.FetchedAt)

	c.updateJobState(providerName, nil)
}

// convertLinesToSnapshots converts OpenUsage lines to store.UsageSnapshot
func (c *Collector) convertLinesToSnapshots(providerID int64, snapshot openusage.UsageSnapshot) []*store.UsageSnapshot {
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
			ProviderID:  providerID,
			Metric:      metric,
			Used:        line.Used,
			Limit:       limit,
			ResetAt:     resetAt,
			CollectedAt: snapshot.FetchedAt,
		}

		snapshots = append(snapshots, snap)
	}

	return snapshots
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