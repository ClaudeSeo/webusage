package ollama

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// sampleCurrentBalanceBody is a current-plan GET /api/balance payload.
const sampleCurrentBalanceBody = `{
  "included": {
    "balance_usd": 40.25,
    "allowance_usd": 50,
    "period": {"from": "2026-08-01T00:00:00Z", "until": "2026-09-01T00:00:00Z"}
  },
  "purchased": {"balance_usd": 10}
}`

// sampleLegacyBalanceBody is a legacy-plan GET /api/balance payload.
const sampleLegacyBalanceBody = `{
  "included": {
    "session": {"remaining_percent": 60, "resets_at": "2026-08-02T12:00:00Z"},
    "weekly": {"remaining_percent": 25, "resets_at": "2026-08-06T00:00:00Z"}
  },
  "purchased": {"balance_usd": 0}
}`

// sampleUsageBody is a GET /api/usage?range=30d payload, abbreviated to one bucket.
const sampleUsageBody = `{
  "range": "30d",
  "scope": "self",
  "granularity": "day",
  "from": "2026-07-02T00:00:00Z",
  "until": "2026-08-01T02:30:00Z",
  "totals": {
    "request_count": 15,
    "usage_usd": 0.01718,
    "input_tokens": 106000,
    "cached_input_tokens": 46000,
    "output_tokens": 13600
  },
  "buckets": [
    {
      "from": "2026-08-01T00:00:00Z",
      "until": "2026-08-01T02:30:00Z",
      "partial": true,
      "request_count": 3,
      "usage_usd": 0.00318,
      "input_tokens": 18000,
      "cached_input_tokens": 6000,
      "output_tokens": 2400
    }
  ]
}`

// newOllamaServer serves balanceBody on /api/balance and usageBody on /api/usage.
func newOllamaServer(t *testing.T, balanceBody, usageBody string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/balance":
			_, _ = w.Write([]byte(balanceBody))
		case "/api/usage":
			_, _ = w.Write([]byte(usageBody))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// urlRewriteDoer redirects the fixed https://ollama.com endpoint to httptest.Server
// so the production URL stays hardcoded and untested code paths stay minimal.
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

// newRewriteDoer points requests at srv.
func newRewriteDoer(srv *httptest.Server) *urlRewriteDoer {
	return &urlRewriteDoer{
		base:   http.DefaultClient,
		scheme: "http",
		host:   strings.TrimPrefix(srv.URL, "http://"),
	}
}

func TestBuildRequestShouldTargetFixedEndpointsWithBearerAuth(t *testing.T) {
	for _, tt := range []struct {
		endpoint string
		path     string
		query    string
	}{
		{balanceEndpoint, "/api/balance", ""},
		{usageEndpoint, "/api/usage", "range=30d"},
	} {
		// Given / When
		req, err := buildRequest(context.Background(), tt.endpoint, "sk-abc")
		if err != nil {
			t.Fatalf("buildRequest(%q): %v", tt.endpoint, err)
		}

		// Then
		if req.Method != http.MethodGet {
			t.Errorf("%s: Method = %q, want GET", tt.path, req.Method)
		}
		if req.URL.Scheme != "https" || req.URL.Host != "ollama.com" || req.URL.Path != tt.path || req.URL.RawQuery != tt.query {
			t.Errorf("URL = %q, want https://ollama.com%s?%s", req.URL.String(), tt.path, tt.query)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer sk-abc" {
			t.Errorf("%s: Authorization = %q, want Bearer sk-abc", tt.path, got)
		}
		// The key must travel in the header only, never in the URL.
		if strings.Contains(req.URL.String(), "sk-abc") {
			t.Errorf("%s: API key leaked into the request URL", tt.path)
		}
	}
}

func TestGetBalanceShouldDecodeCurrentPlanCredits(t *testing.T) {
	// Given: a mock server returning a current-plan balance.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("server got Authorization %q, want Bearer tok", got)
		}
		if r.URL.Path != "/api/balance" {
			t.Errorf("server got path %q, want /api/balance", r.URL.Path)
		}
		_, _ = w.Write([]byte(sampleCurrentBalanceBody))
	}))
	defer srv.Close()

	// When
	resp, err := getBalance(context.Background(), newRewriteDoer(srv), "tok")

	// Then
	if err != nil {
		t.Fatalf("getBalance: %v", err)
	}
	in := resp.Included
	if in.BalanceUSD == nil || *in.BalanceUSD != 40.25 || in.AllowanceUSD == nil || *in.AllowanceUSD != 50 {
		t.Errorf("included = %+v, want balance 40.25 of 50", in)
	}
	if in.Period == nil || in.Period.Until == nil || !in.Period.Until.Equal(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("period = %+v, want until 2026-09-01", in.Period)
	}
	if in.Session != nil || in.Weekly != nil {
		t.Errorf("legacy windows = %+v / %+v, want nil for a current plan", in.Session, in.Weekly)
	}
	if resp.Purchased.BalanceUSD == nil || *resp.Purchased.BalanceUSD != 10 {
		t.Errorf("purchased = %+v, want 10", resp.Purchased)
	}
}

func TestGetBalanceShouldDecodeLegacyPlanWindows(t *testing.T) {
	// Given: a mock server returning a legacy-plan balance.
	srv := newOllamaServer(t, sampleLegacyBalanceBody, sampleUsageBody)
	defer srv.Close()

	// When
	resp, err := getBalance(context.Background(), newRewriteDoer(srv), "tok")

	// Then
	if err != nil {
		t.Fatalf("getBalance: %v", err)
	}
	in := resp.Included
	if in.Session == nil || in.Session.RemainingPercent == nil || *in.Session.RemainingPercent != 60 || in.Session.ResetsAt == nil {
		t.Errorf("session = %+v, want 60%% remaining with a reset time", in.Session)
	}
	if in.Weekly == nil || in.Weekly.RemainingPercent == nil || *in.Weekly.RemainingPercent != 25 || in.Weekly.ResetsAt == nil {
		t.Errorf("weekly = %+v, want 25%% remaining with a reset time", in.Weekly)
	}
	if in.BalanceUSD != nil || in.AllowanceUSD != nil {
		t.Errorf("credit fields = %v / %v, want nil for a legacy plan", in.BalanceUSD, in.AllowanceUSD)
	}
}

func TestGetUsageShouldRequestThirtyDayRangeAndDecodeTotals(t *testing.T) {
	// Given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/usage" || r.URL.Query().Get("range") != "30d" {
			t.Errorf("server got %q, want /api/usage?range=30d", r.URL.String())
		}
		_, _ = w.Write([]byte(sampleUsageBody))
	}))
	defer srv.Close()

	// When
	resp, err := getUsage(context.Background(), newRewriteDoer(srv), "tok")

	// Then
	if err != nil {
		t.Fatalf("getUsage: %v", err)
	}
	if resp.Range != "30d" || resp.Totals.RequestCount != 15 {
		t.Errorf("usage = %+v, want range 30d with 15 requests", resp)
	}
	if resp.Totals.UsageUSD == nil || *resp.Totals.UsageUSD != 0.01718 {
		t.Errorf("usage_usd = %v, want 0.01718", resp.Totals.UsageUSD)
	}
}

func TestGetBalanceShouldReturnErrUnauthorizedOnRejectedKey(t *testing.T) {
	// Given: the API rejects the key.
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		}))

		// When
		_, err := getBalance(context.Background(), newRewriteDoer(srv), "bad")
		srv.Close()

		// Then: a distinct error so the operator knows to fix OLLAMA_API_KEY.
		if err != ErrUnauthorized {
			t.Errorf("status %d: err = %v, want ErrUnauthorized", status, err)
		}
	}
}

func TestGetBalanceShouldErrorWithStatusOnServerFailure(t *testing.T) {
	// Given: transient upstream failures, including rate limiting.
	for _, status := range []int{http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusServiceUnavailable} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		}))

		// When
		_, err := getBalance(context.Background(), newRewriteDoer(srv), "tok")
		srv.Close()

		// Then
		if err == nil || !strings.Contains(err.Error(), strconv.Itoa(status)) {
			t.Errorf("err = %v, want a status %d error", err, status)
		}
	}
}

func TestGetBalanceShouldErrorOnMalformedJSON(t *testing.T) {
	// Given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{not json`))
	}))
	defer srv.Close()

	// When
	_, err := getBalance(context.Background(), newRewriteDoer(srv), "tok")

	// Then
	if err == nil {
		t.Fatal("expected a decode error, got nil")
	}
}

func TestGetBalanceShouldNotLeakAPIKeyInErrors(t *testing.T) {
	// Given: a failing endpoint and a recognizable key.
	const key = "sk-super-secret-key"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	// When
	_, err := getBalance(context.Background(), newRewriteDoer(srv), key)

	// Then: the credential never reaches an error string that may be logged.
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if strings.Contains(err.Error(), key) {
		t.Errorf("error message leaked the API key: %v", err)
	}
}

func TestCollectShouldAbortWhenServiceContextIsCancelledMidRequest(t *testing.T) {
	// Given: a server that holds the request open until the test releases it.
	entered := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
	}))
	defer func() {
		close(release)
		srv.Close()
	}()

	o := &Ollama{apiKey: "tok", httpClient: newRewriteDoer(srv)}
	ctx, cancel := context.WithCancel(context.Background())

	// When: shutdown cancels the service context while the call is in flight.
	go func() {
		<-entered
		cancel()
	}()
	start := time.Now()
	_, err := o.Collect(ctx)
	elapsed := time.Since(start)

	// Then: collection unwinds on cancellation rather than waiting out the
	// client timeout, so shutdown is never blocked by the external call.
	if err == nil {
		t.Fatal("expected a cancellation error, got nil")
	}
	if !strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Errorf("err = %v, want a context cancellation error", err)
	}
	if elapsed >= defaultHTTPClient.Timeout {
		t.Errorf("Collect took %v, want an abort well before the %v client timeout", elapsed, defaultHTTPClient.Timeout)
	}
}

func TestCollectShouldReturnCreditsAndCostWithoutLeakingAPIKeyWhenPlanIsCurrent(t *testing.T) {
	// Given: both endpoints behind a mock server and a recognizable key.
	const key = "sk-super-secret-key"
	srv := newOllamaServer(t, sampleCurrentBalanceBody, sampleUsageBody)
	defer srv.Close()

	o := &Ollama{apiKey: key, httpClient: newRewriteDoer(srv)}

	// When
	metrics, err := o.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	// Then: credits from the balance and cost from the usage report.
	got := byMetric(metrics)
	if len(metrics) != 2 {
		t.Fatalf("metrics = %+v, want credits and cost", metrics)
	}
	if m, ok := got["credits"]; !ok || m.Used != 9.75 {
		t.Errorf("credits = %+v, want used 9.75", m)
	}
	if m, ok := got["cost"]; !ok || m.Used != 0.01718 {
		t.Errorf("cost = %+v, want used 0.01718", m)
	}

	// Then: the raw payload keeps both responses but not the credential or
	// the bulky per-bucket history.
	for _, m := range metrics {
		if strings.Contains(m.RawJSON, key) || strings.Contains(m.RawJSON, "Bearer") {
			t.Errorf("metric %q RawJSON leaked credential material", m.Metric)
		}
		if !strings.Contains(m.RawJSON, `"allowance_usd":50`) || !strings.Contains(m.RawJSON, `"usage_usd":0.01718`) {
			t.Errorf("metric %q RawJSON = %s, want both balance and usage", m.Metric, m.RawJSON)
		}
		if strings.Contains(m.RawJSON, "buckets") {
			t.Errorf("metric %q RawJSON carries buckets: %s", m.Metric, m.RawJSON)
		}
	}
}

func TestCollectShouldReturnSessionAndWeeklyWhenPlanIsLegacy(t *testing.T) {
	// Given: a legacy balance and a usage report with only request counts.
	srv := newOllamaServer(t, sampleLegacyBalanceBody, `{"range":"30d","totals":{"request_count":7},"buckets":[]}`)
	defer srv.Close()

	o := &Ollama{apiKey: "tok", httpClient: newRewriteDoer(srv)}

	// When
	metrics, err := o.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	// Then: the legacy windows land and unpriced usage adds no cost metric.
	got := byMetric(metrics)
	if len(metrics) != 2 {
		t.Fatalf("metrics = %+v, want session and weekly", metrics)
	}
	if m, ok := got["session"]; !ok || m.Used != 40 || m.ResetAt == nil {
		t.Errorf("session = %+v, want used 40 with a reset time", m)
	}
	if m, ok := got["weekly"]; !ok || m.Used != 75 || m.ResetAt == nil {
		t.Errorf("weekly = %+v, want used 75 with a reset time", m)
	}
}

func TestCollectShouldFailWhenBalanceRequestFails(t *testing.T) {
	// Given: the balance is unavailable while the usage report would succeed.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/balance" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(sampleUsageBody))
	}))
	defer srv.Close()

	o := &Ollama{apiKey: "tok", httpClient: newRewriteDoer(srv)}

	// When
	metrics, err := o.Collect(context.Background())

	// Then: no cost-only cycle is persisted without the quota metrics.
	if err == nil || !strings.Contains(err.Error(), "/api/balance") || !strings.Contains(err.Error(), "503") {
		t.Errorf("err = %v, want a /api/balance status 503 error", err)
	}
	if len(metrics) != 0 {
		t.Errorf("metrics = %+v, want none on failure", metrics)
	}
}

func TestCollectShouldFailWhenUsageReportFails(t *testing.T) {
	// Given: the balance succeeds but the usage report is unavailable.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/usage" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(sampleCurrentBalanceBody))
	}))
	defer srv.Close()

	o := &Ollama{apiKey: "tok", httpClient: newRewriteDoer(srv)}

	// When
	metrics, err := o.Collect(context.Background())

	// Then: the cycle is reported as failed rather than silently dropping cost.
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Errorf("err = %v, want a status 503 error", err)
	}
	if len(metrics) != 0 {
		t.Errorf("metrics = %+v, want none on failure", metrics)
	}
}

func TestCollectLiveShouldCallRealAPI(t *testing.T) {
	// Given: a real OLLAMA_API_KEY on this machine. Only when LIVE_TEST=true.
	if os.Getenv("LIVE_TEST") != "true" {
		t.Skip("skipping live test; set LIVE_TEST=true to run")
	}
	key := os.Getenv("OLLAMA_API_KEY")
	if key == "" {
		t.Skip("OLLAMA_API_KEY not set")
	}

	// When
	metrics, err := New(key).Collect(context.Background())

	// Then: metrics come back and no raw payload carries the key.
	if err != nil {
		t.Fatalf("live Collect: %v", err)
	}
	for _, m := range metrics {
		if strings.Contains(m.RawJSON, key) {
			t.Errorf("metric %q RawJSON contains the API key — leak", m.Metric)
		}
		t.Logf("  %s used=%v limit=%v", m.Metric, m.Used, m.Limit)
	}
}
