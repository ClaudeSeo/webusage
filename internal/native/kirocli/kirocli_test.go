package kirocli

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/ClaudeSeo/webusage/internal/native"
	_ "modernc.org/sqlite"
)

// writeKirocliDB creates a temporary data.sqlite3 and inserts the auth_kv token and state profileArn.
// If token is nil, no auth_kv row is inserted. If arn is empty, no state row is inserted.
func writeKirocliDB(t *testing.T, token *authkvToken, arn string) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "data.sqlite3")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS auth_kv (key TEXT PRIMARY KEY, value BLOB)`); err != nil {
		t.Fatalf("create auth_kv: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS state (key TEXT PRIMARY KEY, value BLOB)`); err != nil {
		t.Fatalf("create state: %v", err)
	}
	if token != nil {
		b, _ := json.Marshal(token)
		if _, err := db.Exec(`INSERT INTO auth_kv (key, value) VALUES (?, ?)`, authKVKey, b); err != nil {
			t.Fatalf("insert auth_kv: %v", err)
		}
	}
	if arn != "" {
		prof, _ := json.Marshal(struct {
			Arn         string `json:"arn"`
			ProfileName string `json:"profile_name"`
		}{Arn: arn, ProfileName: "KiroProfile"})
		if _, err := db.Exec(`INSERT INTO state (key, value) VALUES (?, ?)`, profileStateKey, prof); err != nil {
			t.Fatalf("insert state: %v", err)
		}
	}
	return dbPath
}

func validToken() *authkvToken {
	return &authkvToken{
		AccessToken:  "at",
		RefreshToken: "rt",
		ExpiresAt:    time.Now().Add(time.Hour),
		Region:       "ap-northeast-2",
		StartURL:     "https://example.awsapps.com/start",
	}
}

func TestLoadTokenFromDBShouldParseAuthkvSnakeCaseToken(t *testing.T) {
	// Given: auth_kv token in data.sqlite3.
	dbPath := writeKirocliDB(t, validToken(), "")

	// When
	tok, err := loadTokenFromDB(dbPath)

	// Then
	if err != nil {
		t.Fatalf("loadTokenFromDB: %v", err)
	}
	if tok.AccessToken != "at" || tok.Region != "ap-northeast-2" {
		t.Errorf("token = %+v", tok)
	}
}

func TestLoadTokenFromDBShouldErrorWhenTokenMissing(t *testing.T) {
	// Given: no auth_kv row.
	dbPath := writeKirocliDB(t, nil, "")

	// When
	_, err := loadTokenFromDB(dbPath)

	// Then
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestRegionFromProfileArnShouldExtractRegion(t *testing.T) {
	// Given / When / Then
	cases := map[string]string{
		"arn:aws:codewhisperer:us-east-1:000000000000:profile/X": "us-east-1",
		"arn:aws:codewhisperer:eu-central-1:1:profile/Y":         "eu-central-1",
		"arn:aws:codewhisperer:::profile/Z":                      "us-east-1", // empty region -> default
		"not-an-arn":                                             "us-east-1", // parse failure -> default
		"arn:aws:codewhisperer:evil.com/path#:1:profile/X":       "us-east-1", // malicious region -> validation failure -> default (SSRF defense in depth)
	}
	for arn, want := range cases {
		if got := regionFromProfileArn(arn); got != want {
			t.Errorf("regionFromProfileArn(%q) = %q, want %q", arn, got, want)
		}
	}
}

func TestTokenExpiredShouldDetectExpiry(t *testing.T) {
	// Given
	now := time.Now()
	// Then: expired
	if !tokenExpired(&authkvToken{ExpiresAt: now.Add(-time.Minute)}, now) {
		t.Error("expired token should be expired")
	}
	// Then: valid
	if tokenExpired(&authkvToken{ExpiresAt: now.Add(time.Hour)}, now) {
		t.Error("valid token should not be expired")
	}
	// Then: nil
	if !tokenExpired(nil, now) {
		t.Error("nil token should be expired")
	}
}

func TestAvailableShouldReflectAuthkvTokenExistence(t *testing.T) {
	// Given: DB with token.
	dbPath := writeKirocliDB(t, validToken(), "arn:aws:codewhisperer:us-east-1:1:profile/P")
	k := newWithDB(dbPath)
	// Then
	if !k.Available() {
		t.Error("Available() = false, want true when auth_kv token exists")
	}

	// Given: DB without token.
	dbPath2 := writeKirocliDB(t, nil, "")
	k2 := newWithDB(dbPath2)
	if k2.Available() {
		t.Error("Available() = true, want false when no auth_kv token")
	}

	// Given: empty path.
	k3 := newWithDB("")
	if k3.Available() {
		t.Error("Available() = true, want false for empty path")
	}
}

func TestMetricsFromUsageLimitsShouldEmitCreditsWithPrecisionPreferred(t *testing.T) {
	// Given: CREDIT breakdown. Precision preferred.
	reset := float64(1778000000)
	precision := 5427.92
	resp := &usageLimitsResponse{
		NextDateReset: reset,
		UsageBreakdownList: []usageBreakdownItem{{
			ResourceType: "CREDIT", CurrentUsage: 5427, CurrentUsageWithPrecision: &precision,
			UsageLimit: 10000, NextDateReset: reset,
		}},
	}

	// When
	metrics := metricsFromUsageLimits(resp)

	// Then
	if len(metrics) != 1 {
		t.Fatalf("metrics = %d, want 1", len(metrics))
	}
	if metrics[0].Metric != "credits" {
		t.Errorf("metric = %q, want credits", metrics[0].Metric)
	}
	if metrics[0].Used != 5427.92 {
		t.Errorf("used = %v, want 5427.92 (precision)", metrics[0].Used)
	}
	if metrics[0].Limit == nil || *metrics[0].Limit != 10000 {
		t.Errorf("limit = %v, want 10000", metrics[0].Limit)
	}
	if metrics[0].ResetAt == nil || !metrics[0].ResetAt.Equal(time.Unix(1778000000, 0).UTC()) {
		t.Errorf("resetAt = %v", metrics[0].ResetAt)
	}
}

func TestMetricsFromUsageLimitsShouldAggregateMultipleCreditBreakdowns(t *testing.T) {
	// Given: two CREDIT breakdowns (regression guard against aggregation collision).
	r1, r2 := float64(1778000000), float64(1779000000)
	resp := &usageLimitsResponse{
		UsageBreakdownList: []usageBreakdownItem{
			{ResourceType: "CREDIT", CurrentUsage: 10, UsageLimit: 50, NextDateReset: r1},
			{ResourceType: "CREDIT", CurrentUsage: 5, UsageLimit: 20, NextDateReset: r2},
		},
	}

	// When
	metrics := metricsFromUsageLimits(resp)

	// Then: single credits aggregation.
	if len(metrics) != 1 {
		t.Fatalf("metrics = %d, want 1", len(metrics))
	}
	if metrics[0].Used != 15 {
		t.Errorf("used = %v, want 15", metrics[0].Used)
	}
	if metrics[0].Limit == nil || *metrics[0].Limit != 70 {
		t.Errorf("limit = %v, want 70", metrics[0].Limit)
	}
	if metrics[0].ResetAt == nil || !metrics[0].ResetAt.Equal(time.Unix(1779000000, 0).UTC()) {
		t.Errorf("resetAt = %v, want latest epoch", metrics[0].ResetAt)
	}
}

func TestMetricsFromUsageLimitsShouldEmitBonusCreditsWhenFreeTrialActive(t *testing.T) {
	// Given
	expiry := float64(1779000000)
	resp := &usageLimitsResponse{
		UsageBreakdownList: []usageBreakdownItem{{
			ResourceType: "CREDIT", CurrentUsage: 1, UsageLimit: 10,
			FreeTrialInfo: &freeTrialInfo{
				FreeTrialStatus: "ACTIVE", FreeTrialExpiry: expiry,
				CurrentUsage: 100, UsageLimit: 500,
			},
		}},
	}

	// When
	metrics := metricsFromUsageLimits(resp)

	// Then
	byName := map[string]native.Metric{}
	for _, m := range metrics {
		byName[m.Metric] = m
	}
	if byName["bonus_credits"].Used != 100 {
		t.Errorf("bonus_credits used = %v, want 100", byName["bonus_credits"].Used)
	}
}

func TestMetricsFromUsageLimitsShouldAggregateFreeTrialAndActiveBonuses(t *testing.T) {
	// Given
	ftExpiry, bExpiry := float64(1779000000), float64(1780000000)
	resp := &usageLimitsResponse{
		UsageBreakdownList: []usageBreakdownItem{{
			ResourceType: "CREDIT", CurrentUsage: 1, UsageLimit: 10,
			FreeTrialInfo: &freeTrialInfo{
				FreeTrialStatus: "ACTIVE", FreeTrialExpiry: ftExpiry,
				CurrentUsage: 100, UsageLimit: 500,
			},
			Bonuses: []bonusEntry{
				{Status: "ACTIVE", ExpiresAt: bExpiry, CurrentUsage: 5, UsageLimit: 50},
				{Status: "EXHAUSTED", ExpiresAt: bExpiry, CurrentUsage: 999, UsageLimit: 999},
				{Status: "OTHER", ExpiresAt: bExpiry, CurrentUsage: 7, UsageLimit: 7}, // ignored
			},
		}},
	}

	// When
	metrics := metricsFromUsageLimits(resp)

	// Then: freeTrial 100 + ACTIVE 5 + EXHAUSTED 999 = 1104.
	byName := map[string]native.Metric{}
	for _, m := range metrics {
		byName[m.Metric] = m
	}
	if byName["bonus_credits"].Used != 1104 {
		t.Errorf("bonus_credits used = %v, want 1104 (freeTrial 100 + ACTIVE 5 + EXHAUSTED 999)", byName["bonus_credits"].Used)
	}
}

func TestMetricsFromUsageLimitsShouldSkipFreeTrialWhenStatusInactive(t *testing.T) {
	// Given
	resp := &usageLimitsResponse{
		UsageBreakdownList: []usageBreakdownItem{{
			ResourceType: "CREDIT", CurrentUsage: 1, UsageLimit: 10,
			FreeTrialInfo: &freeTrialInfo{FreeTrialStatus: "EXPIRED", CurrentUsage: 100, UsageLimit: 500},
		}},
	}

	// When
	metrics := metricsFromUsageLimits(resp)

	// Then: no bonus_credits.
	for _, m := range metrics {
		if m.Metric == "bonus_credits" {
			t.Error("bonus_credits should not be emitted for inactive free trial")
		}
	}
}

func TestMetricsFromUsageLimitsShouldSkipNonCreditResourceTypes(t *testing.T) {
	// Given
	resp := &usageLimitsResponse{
		UsageBreakdownList: []usageBreakdownItem{
			{ResourceType: "REQUEST", CurrentUsage: 100, UsageLimit: 1000},
		},
	}

	// When
	metrics := metricsFromUsageLimits(resp)

	// Then
	if len(metrics) != 0 {
		t.Errorf("metrics = %d, want 0", len(metrics))
	}
}

func TestMetricsFromUsageLimitsShouldLeaveLimitNullWhenZero(t *testing.T) {
	// Given
	resp := &usageLimitsResponse{
		UsageBreakdownList: []usageBreakdownItem{{
			ResourceType: "CREDIT", CurrentUsage: 7, UsageLimit: 0,
		}},
	}

	// When
	metrics := metricsFromUsageLimits(resp)

	// Then
	if len(metrics) != 1 || metrics[0].Limit != nil {
		t.Errorf("limit = %v, want nil for zero limit", metrics[0].Limit)
	}
}

func TestMetricsFromUsageLimitsShouldHandleZeroNextDateReset(t *testing.T) {
	// Given: nextDateReset 0 (top-level also 0).
	resp := &usageLimitsResponse{
		UsageBreakdownList: []usageBreakdownItem{{
			ResourceType: "CREDIT", CurrentUsage: 7, UsageLimit: 50, NextDateReset: 0,
		}},
	}

	// When
	metrics := metricsFromUsageLimits(resp)

	// Then: resetAt is nil but metric is still emitted.
	if len(metrics) != 1 {
		t.Fatalf("metrics = %d, want 1", len(metrics))
	}
	if metrics[0].ResetAt != nil {
		t.Errorf("resetAt = %v, want nil for zero epoch", metrics[0].ResetAt)
	}
}

func TestMetricsFromUsageLimitsShouldFallBackToTopLevelNextDateReset(t *testing.T) {
	// Given: breakdown nextDateReset 0, top-level present.
	top := float64(1778000000)
	resp := &usageLimitsResponse{
		NextDateReset: top,
		UsageBreakdownList: []usageBreakdownItem{{
			ResourceType: "CREDIT", CurrentUsage: 7, UsageLimit: 50, NextDateReset: 0,
		}},
	}

	// When
	metrics := metricsFromUsageLimits(resp)

	// Then
	if metrics[0].ResetAt == nil || !metrics[0].ResetAt.Equal(time.Unix(1778000000, 0).UTC()) {
		t.Errorf("resetAt = %v, want top-level epoch", metrics[0].ResetAt)
	}
}

func TestCollectShouldReturnErrorWhenTokenExpired(t *testing.T) {
	// Given: expired token.
	expired := validToken()
	expired.ExpiresAt = time.Now().Add(-time.Hour)
	dbPath := writeKirocliDB(t, expired, "arn:aws:codewhisperer:us-east-1:1:profile/P")
	k := newWithDB(dbPath)

	// When
	_, err := k.Collect(context.Background())

	// Then: expired error.
	if err == nil {
		t.Fatal("expected expired error, got nil")
	}
	if err != ErrTokenExpired {
		t.Errorf("err = %v, want ErrTokenExpired", err)
	}
}
