// Package kirocli is a native provider that collects live usage from the
// Kiro CLI management API (https://management.<region>.kiro.dev/Get-Usage-Limits)
// using kiro-cli's IdC token. It collects via network calls with no local cache.
//
// Token source: the auth_kv key 'kirocli:odic:token' in
// ~/Library/Application Support/kiro-cli/data.sqlite3 (the current token that
// kiro-cli refreshes and uses). We only read it and leave refresh to kiro-cli
// (to avoid racing kiro-cli on token file writes).
// The control plane region is extracted from the profileArn region (e.g. us-east-1)
// — it may differ from the token's IDC region (e.g. ap-northeast-2).
// Tokens/secrets must never be exposed in logs or error messages.
package kirocli

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/ClaudeSeo/webusage/internal/native"
)

// ErrUnavailable is returned when the token/profileArn is not present on this machine.
var ErrUnavailable = errors.New("kirocli: token or profile not available on this machine")

// ErrTokenExpired indicates the token has expired and kiro-cli refresh is required.
var ErrTokenExpired = errors.New("kirocli: token expired; run kiro-cli to refresh")

// usageLimitsResponse is the Get-Usage-Limits response. nextDateReset is epoch seconds (float).
type usageLimitsResponse struct {
	NextDateReset        float64              `json:"nextDateReset"` // epoch seconds
	SubscriptionInfo     subscriptionInfo     `json:"subscriptionInfo"`
	OverageConfiguration overageConfiguration `json:"overageConfiguration"`
	UsageBreakdownList   []usageBreakdownItem `json:"usageBreakdownList"`
}

type subscriptionInfo struct {
	SubscriptionTitle string `json:"subscriptionTitle"`
	OverageCapability string `json:"overageCapability"` // e.g. OVERAGE_CAPABLE
}

type overageConfiguration struct {
	OverageStatus string `json:"overageStatus"` // e.g. ENABLED
}

type usageBreakdownItem struct {
	ResourceType              string          `json:"resourceType"`
	DisplayName               string          `json:"displayName"`
	DisplayNamePlural         string          `json:"displayNamePlural"`
	CurrentUsage              float64         `json:"currentUsage"`
	CurrentUsageWithPrecision *float64        `json:"currentUsageWithPrecision"` // precision-preferred
	UsageLimit                float64         `json:"usageLimit"`
	UsageLimitWithPrecision   *float64        `json:"usageLimitWithPrecision"` // precision-preferred
	NextDateReset             float64         `json:"nextDateReset"`           // epoch seconds
	FreeTrialInfo             *freeTrialInfo  `json:"freeTrialInfo"`
	Bonuses                   []bonusEntry    `json:"bonuses"`
	OverageCredits            []overageCredit `json:"overageCredits"`
	AddOnMetadata             *addOnMetadata  `json:"addOnMetadata"`
}

type freeTrialInfo struct {
	FreeTrialStatus           string   `json:"freeTrialStatus"` // e.g. ACTIVE
	FreeTrialExpiry           float64  `json:"freeTrialExpiry"` // epoch seconds
	CurrentUsage              float64  `json:"currentUsage"`
	CurrentUsageWithPrecision *float64 `json:"currentUsageWithPrecision"`
	UsageLimit                float64  `json:"usageLimit"`
	UsageLimitWithPrecision   *float64 `json:"usageLimitWithPrecision"`
}

type bonusEntry struct {
	BonusCode    string  `json:"bonusCode"`
	DisplayName  string  `json:"displayName"`
	Status       string  `json:"status"` // ACTIVE/EXHAUSTED
	ExpiresAt    float64 `json:"expiresAt"`
	CurrentUsage float64 `json:"currentUsage"`
	UsageLimit   float64 `json:"usageLimit"`
}

type overageCredit struct {
	CurrentUsage float64 `json:"currentUsage"`
	UsageLimit   float64 `json:"usageLimit"`
	ExpiresAt    float64 `json:"expiresAt"`
}

type addOnMetadata struct {
	AddOns          []addOnEntry `json:"addOns"`
	AddOnTotalLimit float64      `json:"addOnTotalLimit"`
	AddOnTotalUsage float64      `json:"addOnTotalUsage"`
}

type addOnEntry struct {
	CurrentUsage float64 `json:"currentUsage"`
	UsageLimit   float64 `json:"usageLimit"`
	ExpiresAt    float64 `json:"expiresAt"`
	Source       string  `json:"source"`
}

// Kirocli is the provider that calls the kiro-cli live usage API.
type Kirocli struct {
	dataDBPath string
	httpClient httpDoer
	now        func() time.Time
}

// New creates a Kirocli provider with the default path on this machine.
func New() *Kirocli {
	return &Kirocli{
		dataDBPath: defaultDataDBPath(),
		httpClient: httpDefaultDoer{},
		now:        time.Now,
	}
}

// newWithDB is a seam that injects dataDBPath for testing.
func newWithDB(dataDBPath string) *Kirocli {
	return &Kirocli{
		dataDBPath: dataDBPath,
		httpClient: httpDefaultDoer{},
		now:        time.Now,
	}
}

// Name is the canonical provider ID.
func (k *Kirocli) Name() string { return "kirocli" }

// Available checks whether an accessToken is present in auth_kv at dataDBPath.
// profileArn/expiry are lazy-checked in Collect. Opening the DB to read auth_kv
// means this is not strictly a cheap-check, but it is enough as the 15-minute
// collection gate.
func (k *Kirocli) Available() bool {
	if k.dataDBPath == "" {
		return false
	}
	tok, err := loadTokenFromDB(k.dataDBPath)
	return err == nil && tok != nil && tok.AccessToken != ""
}

// Collect reads the auth_kv token (erroring on expiry) and calls Get-Usage-Limits to collect metrics.
// kiro-cli handles refresh, so an expired token returns ErrTokenExpired.
func (k *Kirocli) Collect(ctx context.Context) ([]native.Metric, error) {
	if k.dataDBPath == "" {
		return nil, ErrUnavailable
	}
	tok, err := loadTokenFromDB(k.dataDBPath)
	if err != nil || tok == nil || tok.AccessToken == "" {
		return nil, ErrUnavailable
	}
	if tokenExpired(tok, k.now()) {
		return nil, ErrTokenExpired
	}

	profileArn, err := resolveProfileArn(k.dataDBPath)
	if err != nil {
		return nil, ErrUnavailable
	}
	region := regionFromProfileArn(profileArn)

	resp, err := getUsageLimits(ctx, k.httpClient, region, tok.AccessToken, profileArn)
	if err != nil {
		return nil, err
	}

	metrics := metricsFromUsageLimits(resp)
	// Marshal only the response so the token never ends up in RawJSON.
	raw := encodeJSON(resp)
	for i := range metrics {
		metrics[i].RawJSON = raw
	}
	return metrics, nil
}

// metricsFromUsageLimits maps the Get-Usage-Limits response into canonical metrics.
//
// credits: sums used/limit of breakdown entries where resourceType == "CREDIT" (single metric).
//   - Same aggregation as kiro.go — prevents rows from being lost due to
//     the collector's idempotent unique index collision.
//   - used = currentUsageWithPrecision ?? currentUsage, limit = usageLimitWithPrecision ?? usageLimit (0 -> nil).
//   - resetAt = breakdown.nextDateReset, falling back to the response's top-level nextDateReset.
//
// bonus_credits: sums used/limit/expiry of freeTrialInfo (ACTIVE) entries plus
// bonuses (ACTIVE/EXHAUSTED) entries (single metric).
//
// overageCredits/addOnMetadata are out of scope for this pass (follow-up PR).
func metricsFromUsageLimits(resp *usageLimitsResponse) []native.Metric {
	if resp == nil {
		return nil
	}
	var creditsUsed, creditsLimit, bonusUsed, bonusLimit float64
	var creditsReset, bonusReset *time.Time
	creditCount, bonusCount := 0, 0

	topReset := epochToTime(resp.NextDateReset)

	for _, b := range resp.UsageBreakdownList {
		if b.ResourceType != "CREDIT" {
			continue
		}
		creditCount++
		creditsUsed += precisionOr(b.CurrentUsage, b.CurrentUsageWithPrecision)
		creditsLimit += precisionOr(b.UsageLimit, b.UsageLimitWithPrecision)
		creditsReset = latestTime(creditsReset, epochToTime(b.NextDateReset))

		if b.FreeTrialInfo != nil && b.FreeTrialInfo.FreeTrialStatus == "ACTIVE" {
			bonusCount++
			bonusUsed += precisionOr(b.FreeTrialInfo.CurrentUsage, b.FreeTrialInfo.CurrentUsageWithPrecision)
			bonusLimit += precisionOr(b.FreeTrialInfo.UsageLimit, b.FreeTrialInfo.UsageLimitWithPrecision)
			bonusReset = latestTime(bonusReset, epochToTime(b.FreeTrialInfo.FreeTrialExpiry))
		}
		for _, bonus := range b.Bonuses {
			if bonus.Status != "ACTIVE" && bonus.Status != "EXHAUSTED" {
				continue
			}
			bonusCount++
			bonusUsed += bonus.CurrentUsage
			bonusLimit += bonus.UsageLimit
			bonusReset = latestTime(bonusReset, epochToTime(bonus.ExpiresAt))
		}
	}

	if creditCount == 0 {
		return nil
	}

	metrics := []native.Metric{{
		Metric:  "credits",
		Used:    creditsUsed,
		Limit:   nonZeroLimit(creditsLimit),
		ResetAt: latestTime(creditsReset, topReset), // fall back to top-level reset when breakdown reset is absent
	}}

	if bonusCount > 0 {
		metrics = append(metrics, native.Metric{
			Metric:  "bonus_credits",
			Used:    bonusUsed,
			Limit:   nonZeroLimit(bonusLimit),
			ResetAt: bonusReset,
		})
	}
	return metrics
}

// precisionOr returns the precision-preferred field when non-nil, otherwise the default.
func precisionOr(v float64, p *float64) float64 {
	if p != nil {
		return *p
	}
	return v
}

// epochToTime converts epoch seconds (float) to a UTC time. Returns nil when 0.
func epochToTime(sec float64) *time.Time {
	if sec == 0 {
		return nil
	}
	t := time.Unix(int64(sec), 0).UTC()
	return &t
}

// latestTime returns the later of a and b. If either is nil, returns the other.
func latestTime(a, b *time.Time) *time.Time {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if b.After(*a) {
		return b
	}
	return a
}

// nonZeroLimit returns a pointer only when v is positive; otherwise nil (0 = unlimited).
func nonZeroLimit(v float64) *float64 {
	if v <= 0 {
		return nil
	}
	return &v
}

// encodeJSON encodes v as a JSON string; returns an empty string on failure (debug only).
func encodeJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}
