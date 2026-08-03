package ollama

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// sampleUsageBody is a real-shaped GET /api/usage payload.
const sampleUsageBody = `{
  "activity": {
    "cost": "0.00000",
    "period": {
      "type": "last_4_weeks",
      "starting_at": "2026-07-06T00:00:00Z",
      "ending_at": "2026-08-01T15:30:39.811115999Z"
    },
    "models": []
  },
  "limits": {
    "session": {
      "usage": 0,
      "models": [{"name": "gemma4:31b", "request_count": 7}]
    },
    "weekly": {
      "usage": 0.365,
      "models": [
        {"name": "glm-5.2", "request_count": 1048},
        {"name": "kimi-k2.6", "request_count": 167},
        {"name": "gemma4:31b", "request_count": 1640}
      ]
    }
  }
}`

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

func TestBuildUsageRequestShouldTargetUsageEndpointWithBearerAuth(t *testing.T) {
	// Given / When
	req, err := buildUsageRequest(context.Background(), "sk-abc")
	if err != nil {
		t.Fatalf("buildUsageRequest: %v", err)
	}

	// Then
	if req.Method != http.MethodGet {
		t.Errorf("Method = %q, want GET", req.Method)
	}
	if req.URL.Scheme != "https" || req.URL.Host != "ollama.com" || req.URL.Path != "/api/usage" {
		t.Errorf("URL = %q, want https://ollama.com/api/usage", req.URL.String())
	}
	if got := req.Header.Get("Authorization"); got != "Bearer sk-abc" {
		t.Errorf("Authorization = %q, want Bearer sk-abc", got)
	}
	// The key must travel in the header only, never in the URL.
	if strings.Contains(req.URL.String(), "sk-abc") {
		t.Error("API key leaked into the request URL")
	}
}

func TestGetUsageShouldDecodeActivityAndLimits(t *testing.T) {
	// Given: a mock server returning the sample payload.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("server got Authorization %q, want Bearer tok", got)
		}
		if r.URL.Path != "/api/usage" {
			t.Errorf("server got path %q, want /api/usage", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sampleUsageBody))
	}))
	defer srv.Close()

	// When
	resp, err := getUsage(context.Background(), newRewriteDoer(srv), "tok")

	// Then
	if err != nil {
		t.Fatalf("getUsage: %v", err)
	}
	if resp.Activity.Cost != "0.00000" {
		t.Errorf("cost = %q, want 0.00000", resp.Activity.Cost)
	}
	if resp.Activity.Period.Type != "last_4_weeks" {
		t.Errorf("period type = %q, want last_4_weeks", resp.Activity.Period.Type)
	}
	if resp.Limits.Session == nil || resp.Limits.Session.Usage == nil || *resp.Limits.Session.Usage != 0 {
		t.Errorf("session usage = %v, want 0", resp.Limits.Session)
	}
	if resp.Limits.Weekly == nil || resp.Limits.Weekly.Usage == nil || *resp.Limits.Weekly.Usage != 0.365 {
		t.Errorf("weekly usage = %v, want 0.365", resp.Limits.Weekly)
	}
	if len(resp.Limits.Weekly.Models) != 3 {
		t.Errorf("weekly models = %d, want 3", len(resp.Limits.Weekly.Models))
	}
}

func TestGetUsageShouldReturnErrUnauthorizedOnRejectedKey(t *testing.T) {
	// Given: the API rejects the key.
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		}))

		// When
		_, err := getUsage(context.Background(), newRewriteDoer(srv), "bad")
		srv.Close()

		// Then: a distinct error so the operator knows to fix OLLAMA_API_KEY.
		if err != ErrUnauthorized {
			t.Errorf("status %d: err = %v, want ErrUnauthorized", status, err)
		}
	}
}

func TestGetUsageShouldErrorOnServerFailure(t *testing.T) {
	// Given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	// When
	_, err := getUsage(context.Background(), newRewriteDoer(srv), "tok")

	// Then
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("err = %v, want a status 500 error", err)
	}
}

func TestGetUsageShouldErrorOnMalformedJSON(t *testing.T) {
	// Given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{not json`))
	}))
	defer srv.Close()

	// When
	_, err := getUsage(context.Background(), newRewriteDoer(srv), "tok")

	// Then
	if err == nil {
		t.Fatal("expected a decode error, got nil")
	}
}

func TestGetUsageShouldNotLeakAPIKeyInErrors(t *testing.T) {
	// Given: a failing endpoint and a recognizable key.
	const key = "sk-super-secret-key"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	// When
	_, err := getUsage(context.Background(), newRewriteDoer(srv), key)

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

func TestCollectShouldReturnMetricsWithoutLeakingAPIKey(t *testing.T) {
	// Given: the sample payload behind a mock server and a recognizable key.
	const key = "sk-super-secret-key"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sampleUsageBody))
	}))
	defer srv.Close()

	o := &Ollama{apiKey: key, httpClient: newRewriteDoer(srv)}

	// When
	metrics, err := o.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	// Then: session, weekly and cost land on the dashboard scale. Presence is
	// asserted explicitly so a dropped metric cannot pass as a zero value.
	got := byMetric(metrics)
	if len(metrics) != 3 {
		t.Fatalf("metrics = %+v, want session, weekly and cost", metrics)
	}
	for _, want := range []struct {
		metric string
		used   float64
	}{
		{"session", 0},
		{"weekly", 36.5},
		{"cost", 0},
	} {
		m, ok := got[want.metric]
		if !ok {
			t.Errorf("%s metric missing", want.metric)
			continue
		}
		if m.Used != want.used {
			t.Errorf("%s used = %v, want %v", want.metric, m.Used, want.used)
		}
	}

	// Then: the credential is absent from every persisted raw payload.
	for _, m := range metrics {
		if strings.Contains(m.RawJSON, key) || strings.Contains(m.RawJSON, "Bearer") {
			t.Errorf("metric %q RawJSON leaked credential material", m.Metric)
		}
		if m.RawJSON == "" {
			t.Errorf("metric %q has no RawJSON", m.Metric)
		}
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
