// Package native defines a self-contained provider that collects usage directly
// from local state (SQLite, log files) without depending on the OpenUsage HTTP API.
// It lets webusage gather data on its own even when OpenUsage is not running.
package native

import "time"

// Provider is a provider that collects usage from local sources without OpenUsage.
// Implementations must be safe for concurrent calls from the collector.
type Provider interface {
	// Name returns the canonical provider ID (lowercase, e.g. "kirocli").
	// It is used as the providers.name row key and the cycle config lookup key.
	Name() string

	// Available reports whether a local data source exists on this machine.
	// The collector skips unavailable providers, so a missing app must not
	// surface as a collection error.
	Available() bool

	// Collect reads local usage state and returns canonical metrics.
	// A nil error with an empty slice means "provider present but no usage yet";
	// the collector logs zero metrics and continues.
	Collect() ([]Metric, error)
}

// Metric is a single canonical usage data point emitted by a native provider.
// ProviderID is assigned by the collector after resolving the DB provider row,
// so the provider leaves it as 0.
type Metric struct {
	ProviderID int64

	Metric  string // canonical lowercase metric key, e.g. "credits"
	Used    float64
	Limit   *float64
	ResetAt *time.Time

	// RawJSON is the original payload for debugging and auditing.
	RawJSON string
}
