package collector

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ClaudeSeo/webusage/internal/provider"
	"github.com/ClaudeSeo/webusage/internal/store"
)

// Collector manages scheduled usage data collection
type Collector struct {
	store          *store.Store
	registry       *provider.Registry
	providers      map[string]provider.Provider
	interval       time.Duration
	logger         *slog.Logger
	jobLocks       sync.Map
	maxRetries     int
	initialBackoff time.Duration
	maxTimeout     time.Duration
}

// JobState tracks the state of a collection job
type JobState struct {
	running    atomic.Int32 // 0=idle, 1=running — CAS로 race condition 방지
	LastRun    time.Time
	LastError  error
	RetryCount int
}

// NewCollector creates a new usage collector
func NewCollector(
	s *store.Store,
	providers []provider.Provider,
	interval time.Duration,
	logger *slog.Logger,
	registry *provider.Registry,
) *Collector {
	provMap := make(map[string]provider.Provider)
	for _, p := range providers {
		provMap[p.Name()] = p
	}

	return &Collector{
		store:          s,
		registry:       registry,
		providers:      provMap,
		interval:       interval,
		logger:         logger,
		maxRetries:     3,
		initialBackoff: 5 * time.Second,
		maxTimeout:     60 * time.Second, // Per-job timeout
	}
}

// Start begins the collection loop
func (c *Collector) Start(ctx context.Context) error {
	c.logger.Info("Starting usage collector",
		"interval", c.interval.String(),
		"providers", len(c.providers))

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

// collectAll triggers collection for all enabled providers
func (c *Collector) collectAll(ctx context.Context) error {
	enabledProviders, err := c.store.ListProviders()
	if err != nil {
		return fmt.Errorf("listing providers: %w", err)
	}

	var wg sync.WaitGroup
	for _, p := range enabledProviders {
		if !p.Enabled {
			continue
		}

		wg.Add(1)
		go func(p *store.Provider) {
			defer wg.Done()
			c.collectProvider(ctx, p)
		}(p)
	}

	wg.Wait()
	return nil
}

// collectProvider collects usage for a single provider with retry logic
func (c *Collector) collectProvider(ctx context.Context, p *store.Provider) {
	// Check lock to prevent duplicate runs
	if !c.tryLock(p.Name) {
		c.logger.Debug("Skipping collection - already running", "provider", p.Name)
		return
	}
	defer c.unlock(p.Name)

	prov, exists := c.providers[p.Name]
	if !exists {
		c.logger.Warn("Provider not found in registry", "provider", p.Name)
		return
	}

	// Execute with retry, exponential backoff, and jitter
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			backoff := c.calculateBackoffWithJitter(attempt)
			c.logger.Info("Retrying collection",
				"provider", p.Name,
				"attempt", attempt+1,
				"backoff", backoff.String())

			// Wait with context cancellation support
			select {
			case <-time.After(backoff):
				// Continue to retry
			case <-ctx.Done():
				c.logger.Warn("Collection cancelled", "provider", p.Name)
				return
			}
		}

		// Create timeout context for this attempt
		jobCtx, cancel := context.WithTimeout(ctx, c.maxTimeout)
		lastErr = c.doCollect(jobCtx, p.ID, prov)
		cancel()

		if lastErr == nil {
			// Success - update status
			if err := c.store.UpdateProviderStatus(p.ID, nil); err != nil {
				c.logger.Error("Failed to update provider status", "error", err)
			}
			return
		}

		// Check if context was cancelled
		if ctx.Err() != nil {
			c.logger.Warn("Collection cancelled during retry", "provider", p.Name)
			return
		}

		// Auth 에러(401/403)는 재시도 없이 즉시 비활성화
		if isAuthError(lastErr) {
			c.logger.Warn("Auth error detected - disabling provider",
				"provider", p.Name,
				"error", lastErr)
			c.disableProvider(p)
			return
		}

		c.logger.Warn("Collection attempt failed",
			"provider", p.Name,
			"attempt", attempt+1,
			"error", lastErr)
	}

	// All retries exhausted - update status with error
	errMsg := lastErr.Error()
	if err := c.store.UpdateProviderStatus(p.ID, &errMsg); err != nil {
		c.logger.Error("Failed to update provider status with error", "error", err)
	}
}

// isAuthError는 에러가 401/403 인증 실패인지 판별합니다
func isAuthError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "401") || strings.Contains(msg, "403")
}

// disableProvider는 DB와 Registry 양쪽에서 provider를 비활성화하고 last_error를 기록합니다
func (c *Collector) disableProvider(p *store.Provider) {
	errMsg := "disabled due to authentication error (401/403)"

	if err := c.store.UpdateProviderStatus(p.ID, &errMsg); err != nil {
		c.logger.Error("Failed to update provider status", "provider", p.Name, "error", err)
	}

	if err := c.store.EnableProvider(p.ID, false); err != nil {
		c.logger.Error("Failed to disable provider in DB", "provider", p.Name, "error", err)
	}

	if c.registry != nil {
		if err := c.registry.SetEnabled(p.Name, false); err != nil {
			c.logger.Error("Failed to disable provider in registry", "provider", p.Name, "error", err)
		}
	}
}

// doCollect performs the actual data collection with idempotency check
func (c *Collector) doCollect(ctx context.Context, providerID int64, prov provider.Provider) error {
	usagePoints, err := prov.FetchUsage(ctx)
	if err != nil {
		return err
	}

	// Convert provider.UsagePoint to store.UsageSnapshot
	snapshots := make([]*store.UsageSnapshot, len(usagePoints))
	for i, up := range usagePoints {
		snapshots[i] = &store.UsageSnapshot{
			ProviderID:  providerID,
			Metric:      up.Metric,
			Used:        up.Used,
			Limit:       up.Limit,
			ResetAt:     up.ResetAt,
			CollectedAt: up.CollectedAt,
			RawJSON:     up.RawJSON,
		}
	}

	// Store in database with idempotency check
	if err := c.store.CreateUsageSnapshotsIdempotent(snapshots); err != nil {
		return fmt.Errorf("storing snapshots: %w", err)
	}

	c.logger.Info("Collected usage data",
		"provider_id", providerID,
		"metrics", len(snapshots))

	return nil
}

// tryLock attempts to acquire a lock for a provider — atomic CAS로 race condition 방지
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

// calculateBackoffWithJitter computes exponential backoff with random jitter
// Formula: min(base * 2^attempt + random(0, jitter), maxBackoff)
func (c *Collector) calculateBackoffWithJitter(attempt int) time.Duration {
	// Exponential backoff: base * 2^(attempt-1)
	multiplier := 1
	for i := 1; i < attempt; i++ {
		multiplier *= 2
	}

	baseBackoff := c.initialBackoff * time.Duration(multiplier)

	// Add jitter: random value between 0 and baseBackoff * 0.5
	jitterMax := baseBackoff / 2
	jitter, _ := rand.Int(rand.Reader, big.NewInt(int64(jitterMax)))

	totalBackoff := baseBackoff + time.Duration(jitter.Int64())

	// Cap at maximum (5 minutes)
	maxBackoff := 5 * time.Minute
	if totalBackoff > maxBackoff {
		totalBackoff = maxBackoff
	}

	return totalBackoff
}

// GetJobStates returns the current state of all collection jobs
func (c *Collector) GetJobStates() map[string]*JobState {
	states := make(map[string]*JobState)
	c.jobLocks.Range(func(key, value interface{}) bool {
		states[key.(string)] = value.(*JobState)
		return true
	})
	return states
}
