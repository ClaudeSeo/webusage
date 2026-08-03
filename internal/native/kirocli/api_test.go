package kirocli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// urlRewriteDoer rewrites the request URL to the test server host so that
// buildUsageLimitsRequest's fixed host (management.<region>.kiro.dev) is sent to httptest.Server.
type urlRewriteDoer struct {
	base   httpDoer
	scheme string
	host   string
}

func (d *urlRewriteDoer) Do(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = d.scheme
	req.URL.Host = d.host
	req.Host = d.host
	return d.base.Do(req)
}

func TestBuildUsageLimitsRequestShouldSetMethodPathQueryAndHeaders(t *testing.T) {
	// Given / When
	req, err := buildUsageLimitsRequest("us-east-1", "abc", "arn:aws:codewhisperer:us-east-1:123:profile/X")
	if err != nil {
		t.Fatalf("buildUsageLimitsRequest: %v", err)
	}

	// Then
	if req.Method != http.MethodGet {
		t.Errorf("Method = %q, want GET", req.Method)
	}
	if req.URL.Host != "management.us-east-1.kiro.dev" {
		t.Errorf("Host = %q, want management.us-east-1.kiro.dev", req.URL.Host)
	}
	if req.URL.Path != "/Get-Usage-Limits" {
		t.Errorf("Path = %q, want /Get-Usage-Limits", req.URL.Path)
	}
	if got := req.URL.Query().Get("origin"); got != "KIRO_CLI" {
		t.Errorf("origin = %q, want KIRO_CLI", got)
	}
	if got := req.URL.Query().Get("profileArn"); got != "arn:aws:codewhisperer:us-east-1:123:profile/X" {
		t.Errorf("profileArn = %q, want arn", got)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer abc" {
		t.Errorf("Authorization = %q, want Bearer abc", got)
	}
	if got := req.Header.Get("TokenType"); got != "SSO_OIDC" {
		t.Errorf("TokenType = %q, want SSO_OIDC", got)
	}
}

func TestGetUsageLimitsShouldDecodeResponseAgainstMockServer(t *testing.T) {
	// Given: canned response (mock server). nextDateReset is float (epoch seconds).
	canned := `{"nextDateReset":1778000000.0,"usageBreakdownList":[{"resourceType":"CREDIT","currentUsage":3,"currentUsageWithPrecision":3.5,"usageLimit":50,"nextDateReset":1778000000.0}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("server got Authorization %q, want Bearer tok", r.Header.Get("Authorization"))
		}
		if r.Header.Get("TokenType") != "SSO_OIDC" {
			t.Errorf("server got TokenType %q, want SSO_OIDC", r.Header.Get("TokenType"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(canned))
	}))
	defer srv.Close()

	doer := &urlRewriteDoer{base: http.DefaultClient, scheme: "http", host: strings.TrimPrefix(srv.URL, "http://")}

	// When
	resp, err := getUsageLimits(context.Background(), doer, "us-east-1", "tok", "arn")

	// Then
	if err != nil {
		t.Fatalf("getUsageLimits: %v", err)
	}
	if resp.NextDateReset != 1778000000.0 {
		t.Errorf("nextDateReset = %v, want 1778000000.0", resp.NextDateReset)
	}
	if len(resp.UsageBreakdownList) != 1 {
		t.Errorf("usageBreakdownList len = %d, want 1", len(resp.UsageBreakdownList))
	}
}

func TestGetUsageLimitsShouldErrorOnNon200Response(t *testing.T) {
	// Given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	doer := &urlRewriteDoer{base: http.DefaultClient, scheme: "http", host: strings.TrimPrefix(srv.URL, "http://")}

	// When
	_, err := getUsageLimits(context.Background(), doer, "us-east-1", "tok", "arn")

	// Then
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("err = %v, want status 500", err)
	}
}

func TestGetUsageLimitsShouldErrorOnMalformedJSON(t *testing.T) {
	// Given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{not json`))
	}))
	defer srv.Close()
	doer := &urlRewriteDoer{base: http.DefaultClient, scheme: "http", host: strings.TrimPrefix(srv.URL, "http://")}

	// When
	_, err := getUsageLimits(context.Background(), doer, "us-east-1", "tok", "arn")

	// Then
	if err == nil {
		t.Fatal("expected decode error, got nil")
	}
}

func TestCollectShouldReturnMetricsWhenLiveCallsMocked(t *testing.T) {
	// Given: canned response mocking + auth_kv token (valid) + profileArn sqlite.
	canned := `{"nextDateReset":1778000000.0,"usageBreakdownList":[{"resourceType":"CREDIT","currentUsage":3,"currentUsageWithPrecision":5427.92,"usageLimit":10000,"nextDateReset":1778000000.0}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the request went to management.us-east-1.kiro.dev (profileArn region).
		if r.URL.Query().Get("profileArn") == "" {
			t.Error("profileArn query missing")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(canned))
	}))
	defer srv.Close()

	tok := validToken()
	dbPath := writeKirocliDB(t, tok, "arn:aws:codewhisperer:us-east-1:1:profile/P")

	k := &Kirocli{
		dataDBPath: dbPath,
		httpClient: &urlRewriteDoer{base: http.DefaultClient, scheme: "http", host: strings.TrimPrefix(srv.URL, "http://")},
		now:        time.Now,
	}

	// When
	metrics, err := k.Collect(context.Background())

	// Then: 1 credits. No token in RawJSON.
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(metrics) != 1 || metrics[0].Metric != "credits" {
		t.Fatalf("metrics = %+v, want 1 credits", metrics)
	}
	if metrics[0].Used != 5427.92 {
		t.Errorf("used = %v, want 5427.92", metrics[0].Used)
	}
	for _, m := range metrics {
		if strings.Contains(m.RawJSON, "Authorization") || strings.Contains(m.RawJSON, "Bearer") {
			t.Error("RawJSON must not contain request auth markers")
		}
	}
}

func TestCollectLiveShouldCallRealAPI(t *testing.T) {
	// Given: real data.sqlite3 on this machine. Only when LIVE_TEST=true.
	if os.Getenv("LIVE_TEST") != "true" {
		t.Skip("skipping live test; set LIVE_TEST=true to run")
	}

	// When
	metrics, err := New().Collect(context.Background())

	// Then: returns metric without error + token substring must not appear in RawJSON.
	if err != nil {
		t.Fatalf("live Collect: %v", err)
	}
	if len(metrics) == 0 {
		t.Skip("no metrics returned")
	}
	for _, m := range metrics {
		if strings.Contains(m.RawJSON, "access_token") || strings.Contains(m.RawJSON, "refresh_token") {
			t.Errorf("metric %q RawJSON contains token field — leak", m.Metric)
		}
	}
	t.Logf("live metrics: %d", len(metrics))
	for _, m := range metrics {
		t.Logf("  %s used=%v limit=%v reset=%v", m.Metric, m.Used, m.Limit, m.ResetAt)
	}
}
