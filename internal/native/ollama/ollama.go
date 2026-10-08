// Package ollama is a native provider that collects Ollama Cloud usage from
// https://ollama.com/api/balance and https://ollama.com/api/usage. Unlike the
// other native providers it has no local state to read: the account is
// identified solely by the OLLAMA_API_KEY environment variable, which the
// composition root passes to New.
//
// The key is sent only in the Authorization header and must never appear in
// logs, errors, or the stored RawJSON.
package ollama

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/ClaudeSeo/webusage/internal/native"
)

// ErrUnavailable is returned when no API key is configured on this machine.
var ErrUnavailable = errors.New("ollama: OLLAMA_API_KEY is not configured")

// percentLimit is the denominator for legacy-plan windows. The API reports
// remaining_percent on a 0-100 scale, so consumption is stored as 100 minus
// that value against limit 100, matching the dashboard's convention for
// ratio-shaped metrics.
const percentLimit = 100.0

// balanceResponse is the GET /api/balance response.
type balanceResponse struct {
	Included  includedBalance  `json:"included"`
	Purchased purchasedBalance `json:"purchased"`
}

// includedBalance is a union of two plan shapes, decoded into one struct and
// told apart by which fields are present. Current plans report USD credits
// (BalanceUSD, AllowanceUSD, Period); legacy plans report Session and Weekly
// windows. Every field is a pointer so an absent field stays distinguishable
// from a reported zero.
type includedBalance struct {
	BalanceUSD   *float64      `json:"balance_usd,omitempty"`
	AllowanceUSD *float64      `json:"allowance_usd,omitempty"`
	Period       *creditPeriod `json:"period,omitempty"`
	Session      *legacyWindow `json:"session,omitempty"`
	Weekly       *legacyWindow `json:"weekly,omitempty"`
}

// creditPeriod is the monthly window of the included credits; Until is the
// exclusive end and therefore the next reset.
type creditPeriod struct {
	From  *time.Time `json:"from,omitempty"`
	Until *time.Time `json:"until,omitempty"`
}

// legacyWindow is a legacy-plan limit window.
type legacyWindow struct {
	RemainingPercent *float64   `json:"remaining_percent,omitempty"` // 0-100
	ResetsAt         *time.Time `json:"resets_at,omitempty"`
}

// purchasedBalance is the unexpired purchased credit balance. It is a balance
// with no allowance to measure it against, so it is kept in RawJSON only:
// stored as Used it would read as usage that shrinks while credits are spent.
type purchasedBalance struct {
	BalanceUSD *float64 `json:"balance_usd,omitempty"`
}

// usageResponse is the GET /api/usage?range=30d response. buckets is not
// decoded: the totals carry every value mapped to a metric, and leaving the
// 31 daily buckets out keeps them from bloating the stored RawJSON.
type usageResponse struct {
	Range       string       `json:"range"`
	Scope       string       `json:"scope"`
	Granularity string       `json:"granularity"`
	From        *time.Time   `json:"from,omitempty"`
	Until       *time.Time   `json:"until,omitempty"`
	Totals      usageMetrics `json:"totals"`
}

// usageMetrics holds the range totals. Legacy-plan requests carry only
// request counts, so the priced and token fields are optional.
type usageMetrics struct {
	RequestCount      int64    `json:"request_count"`
	UsageUSD          *float64 `json:"usage_usd,omitempty"`
	InputTokens       *int64   `json:"input_tokens,omitempty"`
	CachedInputTokens *int64   `json:"cached_input_tokens,omitempty"`
	OutputTokens      *int64   `json:"output_tokens,omitempty"`
}

// rawPayload is the RawJSON shape: both decoded responses, never the request.
type rawPayload struct {
	Balance *balanceResponse `json:"balance"`
	Usage   *usageResponse   `json:"usage"`
}

// Ollama is the provider that calls the Ollama Cloud account APIs.
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

// Collect calls the balance and usage APIs and maps them to canonical metrics.
// Both calls must succeed: a partial result would persist a cycle with cost
// silently missing. ctx carries the service lifetime so shutdown is not
// blocked by either request.
func (o *Ollama) Collect(ctx context.Context) ([]native.Metric, error) {
	if o.apiKey == "" {
		return nil, ErrUnavailable
	}

	balance, err := getBalance(ctx, o.httpClient, o.apiKey)
	if err != nil {
		return nil, err
	}
	usage, err := getUsage(ctx, o.httpClient, o.apiKey)
	if err != nil {
		return nil, err
	}

	metrics := metricsFromBalance(balance)
	if cost, ok := costMetric(usage); ok {
		metrics = append(metrics, cost)
	}
	// Marshal only the decoded responses so no request credential can reach RawJSON.
	raw := encodeJSON(rawPayload{Balance: balance, Usage: usage})
	for i := range metrics {
		metrics[i].RawJSON = raw
	}
	return metrics, nil
}

// metricsFromBalance maps the included balance into canonical metrics.
//
// credits (current plans): allowance_usd - balance_usd spent against limit
// allowance_usd, resetting at period.until. Skipped without a positive
// allowance, since there is then no quota position to report.
//
// session / weekly (legacy plans): 100 - remaining_percent against limit 100,
// resetting at resets_at. The keys match the earlier /api/usage ratios so the
// stored history stays continuous. A window that reports no remaining value is
// skipped so it is never persisted as 100% consumed.
func metricsFromBalance(resp *balanceResponse) []native.Metric {
	if resp == nil {
		return nil
	}

	var metrics []native.Metric
	in := resp.Included
	if in.BalanceUSD != nil && in.AllowanceUSD != nil && *in.AllowanceUSD > 0 {
		limit := *in.AllowanceUSD
		m := native.Metric{Metric: "credits", Used: limit - *in.BalanceUSD, Limit: &limit}
		if in.Period != nil {
			m.ResetAt = in.Period.Until
		}
		metrics = append(metrics, m)
	}
	if m, ok := legacyMetric("session", in.Session); ok {
		metrics = append(metrics, m)
	}
	if m, ok := legacyMetric("weekly", in.Weekly); ok {
		metrics = append(metrics, m)
	}
	return metrics
}

// legacyMetric builds a consumed-percent metric from a legacy window.
// The second return is false when the window reports no remaining value.
func legacyMetric(name string, w *legacyWindow) (native.Metric, bool) {
	if w == nil || w.RemainingPercent == nil {
		return native.Metric{}, false
	}
	limit := percentLimit
	return native.Metric{
		Metric:  name,
		Used:    percentLimit - *w.RemainingPercent,
		Limit:   &limit,
		ResetAt: w.ResetsAt,
	}, true
}

// costMetric maps the 30-day usage_usd total into a limit-free spend metric.
// The total values every request in the range, both plan-covered and paid
// from purchased credits. It is absent when the range includes legacy-plan
// requests; that is dropped rather than stored as 0, which would understate
// real spend.
func costMetric(resp *usageResponse) (native.Metric, bool) {
	if resp == nil || resp.Totals.UsageUSD == nil {
		return native.Metric{}, false
	}
	return native.Metric{Metric: "cost", Used: *resp.Totals.UsageUSD}, true
}

// encodeJSON encodes v as a JSON string; returns an empty string on failure (debug only).
func encodeJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}
