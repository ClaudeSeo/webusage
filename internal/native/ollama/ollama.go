// Package ollama is a native provider that collects Ollama Cloud usage from
// https://ollama.com/api/usage. Unlike the other native providers it has no
// local state to read: the account is identified solely by the OLLAMA_API_KEY
// environment variable, which the composition root passes to New.
//
// The key is sent only in the Authorization header and must never appear in
// logs, errors, or the stored RawJSON.
package ollama

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/ClaudeSeo/webusage/internal/native"
)

// ErrUnavailable is returned when no API key is configured on this machine.
var ErrUnavailable = errors.New("ollama: OLLAMA_API_KEY is not configured")

// usageRatioScale converts the API's [0,1] limit ratio into the 0-100
// percentage convention the dashboard already stores for ratio-shaped metrics
// (used/limit*100 against limit=100), so Ollama compares directly with the
// OpenUsage-sourced session and premium metrics.
//
// The [0,1] contract is what Ollama's own client applies to this field:
// `fmt.Fprintf(table, "  Used\t%.1f%%\n", limit.Usage*100)`, with a golden test
// rendering 0.006 as "0.6%". Values above 1 are left unclamped so an account
// drawing on purchased extra usage reads as over 100% instead of silently
// pinning at the cap.
const usageRatioScale = 100.0

// ratioLimit is the denominator paired with a scaled ratio metric.
const ratioLimit = 100.0

// usageResponse is the GET /api/usage response.
type usageResponse struct {
	Activity activity `json:"activity"`
	Limits   limits   `json:"limits"`
}

// activity is the spend summary for a trailing reporting window.
type activity struct {
	Cost   string         `json:"cost"` // decimal USD string, e.g. "0.00000"
	Period activityPeriod `json:"period"`
	Models []modelUsage   `json:"models"`
}

// activityPeriod is the window the activity figures cover. It is a trailing
// report window (e.g. last_4_weeks ending "now"), not a limit reset boundary,
// so it is never mapped onto Metric.ResetAt.
type activityPeriod struct {
	Type       string     `json:"type"`
	StartingAt *time.Time `json:"starting_at"`
	EndingAt   *time.Time `json:"ending_at"`
}

// limits holds the per-window consumption ratios. Buckets are pointers so an
// absent window stays distinguishable from a window reporting zero usage.
type limits struct {
	Session *limitBucket `json:"session"`
	Weekly  *limitBucket `json:"weekly"`
}

type limitBucket struct {
	Usage  *float64     `json:"usage"` // fraction of the plan limit, in [0,1]
	Models []modelUsage `json:"models"`
}

// modelUsage is the per-model breakdown. The store has no model dimension, so
// these are preserved in RawJSON rather than emitted as metrics. Request counts
// are also not convertible into a quota position: usage is weighted by model and
// token volume, so two models consume the same window at different rates.
type modelUsage struct {
	Name         string `json:"name"`
	RequestCount int64  `json:"request_count"`
	Cost         string `json:"cost,omitempty"` // activity models only
}

// Ollama is the provider that calls the Ollama Cloud usage API.
type Ollama struct {
	apiKey     string
	httpClient httpDoer
}

// New creates an Ollama provider for the given API key. An empty key yields a
// provider that reports itself unavailable, so an unconfigured account is
// skipped instead of failing every collection cycle.
func New(apiKey string) *Ollama {
	return &Ollama{
		apiKey:     strings.TrimSpace(apiKey),
		httpClient: httpDefaultDoer{},
	}
}

// Name is the canonical provider ID.
func (o *Ollama) Name() string { return "ollama" }

// Available reports whether an API key is configured. There is no local state
// to probe, so this is a pure in-memory check.
func (o *Ollama) Available() bool { return o.apiKey != "" }

// Collect calls the usage API and maps the response to canonical metrics.
// ctx carries the service lifetime so shutdown is not blocked by the request.
func (o *Ollama) Collect(ctx context.Context) ([]native.Metric, error) {
	if o.apiKey == "" {
		return nil, ErrUnavailable
	}

	resp, err := getUsage(ctx, o.httpClient, o.apiKey)
	if err != nil {
		return nil, err
	}

	metrics := metricsFromUsage(resp)
	// Marshal only the decoded response so no request credential can reach RawJSON.
	raw := encodeJSON(resp)
	for i := range metrics {
		metrics[i].RawJSON = raw
	}
	return metrics, nil
}

// metricsFromUsage maps the usage response into canonical metrics.
//
// session / weekly: limits.<window>.usage scaled from a [0,1] ratio to 0-100
// with limit 100. A window that reports no usage value is skipped so an
// unreported limit is never persisted as 0%.
//
// cost: activity.cost parsed from its decimal USD string, with no limit — the
// endpoint publishes no spend cap. An absent or malformed value is dropped
// rather than stored as 0, which would understate real spend. It is a separate
// meter from the limit ratios: spend accrues over activity.period (a trailing
// four weeks) while the ratios track plan quota over 5h and 7d windows.
//
// ResetAt is left nil throughout: the payload carries no limit reset timestamp
// in any field, and activity.period is a trailing report window rather than a
// reset boundary. Cycle boundaries therefore come from the provider's cycle
// config in internal/domain.
func metricsFromUsage(resp *usageResponse) []native.Metric {
	if resp == nil {
		return nil
	}

	var metrics []native.Metric
	if m, ok := ratioMetric("session", resp.Limits.Session); ok {
		metrics = append(metrics, m)
	}
	if m, ok := ratioMetric("weekly", resp.Limits.Weekly); ok {
		metrics = append(metrics, m)
	}
	if cost, ok := parseCost(resp.Activity.Cost); ok {
		metrics = append(metrics, native.Metric{Metric: "cost", Used: cost})
	}
	return metrics
}

// parseCost parses the decimal cost string, rejecting non-finite values along
// with malformed ones. ParseFloat accepts "NaN" and "Inf", and either would
// make json.Marshal fail for the whole /api/current response — one upstream
// field change would take down every provider's view, not just this metric.
func parseCost(s string) (float64, bool) {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

// ratioMetric builds a scaled percentage metric from a limit bucket.
// The second return is false when the window reports no usage value.
func ratioMetric(name string, bucket *limitBucket) (native.Metric, bool) {
	if bucket == nil || bucket.Usage == nil {
		return native.Metric{}, false
	}
	limit := ratioLimit
	return native.Metric{
		Metric: name,
		Used:   *bucket.Usage * usageRatioScale,
		Limit:  &limit,
	}, true
}

// encodeJSON encodes v as a JSON string; returns an empty string on failure (debug only).
func encodeJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}
