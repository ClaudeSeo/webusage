package http

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ClaudeSeo/webusage/internal/domain"
	"github.com/ClaudeSeo/webusage/internal/store"
)

func newTempActivityServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "usage.db")
	db, err := store.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server, err := NewServer(db, "127.0.0.1", 0, logger, "../../templates")
	if err != nil {
		db.Close()
		t.Fatalf("NewServer() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return server, db
}

func TestActivityEndpointReturnsCompleteKSTCoverageAndConfiguredInterval(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()
	server.SetCollectionInterval(17 * time.Minute)
	providerID := mustCreateHTTPTestProvider(t, server, "activity", `{"auth_method":"oauth_file","cred_source":"/secret/token","base_url":"https://secret.example"}`)
	loc, err := time.LoadLocation("Asia/Seoul")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().In(loc)
	collection := time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), 5, 0, 0, loc).UTC()
	mustCreateHTTPTestSnapshots(t, server, []*store.UsageSnapshot{
		{ProviderID: providerID, Metric: "session", Used: 1, CollectedAt: collection},
		{ProviderID: providerID, Metric: "weekly", Used: 2, CollectedAt: collection},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/activity", nil)
	w := httptest.NewRecorder()
	server.mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", w.Code, w.Body.String())
	}
	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	cells, ok := response["cells"].([]interface{})
	if !ok || len(cells) != 336 {
		t.Fatalf("cells = %#v, want 336 cells", response["cells"])
	}
	if got := int(response["total_snapshots"].(float64)); got != 2 {
		t.Fatalf("total_snapshots = %d, want 2", got)
	}
	if got := int(response["collection_interval_seconds"].(float64)); got != 17*60 {
		t.Fatalf("collection interval = %d, want %d", got, 17*60)
	}
	if got := response["timezone"]; got != "Asia/Seoul" {
		t.Fatalf("timezone = %v, want Asia/Seoul", got)
	}
}

func TestCurrentEndpointAddsMetricProjectionWithoutLeakingProviderSecrets(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()
	providerID := mustCreateHTTPTestProvider(t, server, "claude", `{"auth_method":"oauth_file","cred_source":"/secret/token","base_url":"https://secret.example","api_key":"super-secret"}`)
	mustEnableHTTPTestProvider(t, server, "claude")
	now := time.Now().UTC()
	limit := 100.0
	mustCreateHTTPTestSnapshots(t, server, []*store.UsageSnapshot{
		{ProviderID: providerID, Metric: "session", Used: 20, Limit: &limit, ResetAt: timePtr(now.Add(2 * time.Hour)), CollectedAt: now.Add(-time.Hour)},
		{ProviderID: providerID, Metric: "session", Used: 40, Limit: &limit, ResetAt: timePtr(now.Add(2 * time.Hour)), CollectedAt: now},
		{ProviderID: providerID, Metric: "weekly", Used: 80, Limit: &limit, ResetAt: timePtr(now.Add(24 * time.Hour)), CollectedAt: now},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/current", nil)
	w := httptest.NewRecorder()
	server.mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, secret := range []string{"/secret/token", "secret.example", "super-secret"} {
		if strings.Contains(body, secret) {
			t.Fatalf("response leaked %q: %s", secret, body)
		}
	}
	var response map[string]map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	metrics, ok := response["claude"]["metrics"].(map[string]interface{})
	if !ok || metrics["session"] == nil || metrics["weekly"] == nil {
		t.Fatalf("metric projections = %#v", response["claude"]["metrics"])
	}
}

func TestActivityEndpointDetectsUniqueCollectionGaps(t *testing.T) {
	server, db := newTempActivityServer(t)
	server.SetCollectionInterval(15 * time.Minute)
	providerID, err := db.CreateProvider("activity-gap", `{}`)
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}
	loc, _ := time.LoadLocation("Asia/Seoul")
	first := time.Date(2026, 8, 1, 10, 0, 0, 0, loc)
	second := first.Add(time.Hour)
	if err := db.CreateUsageSnapshots([]*store.UsageSnapshot{
		{ProviderID: providerID, Metric: "session", Used: 1, CollectedAt: first},
		{ProviderID: providerID, Metric: "weekly", Used: 2, CollectedAt: first},
		{ProviderID: providerID, Metric: "session", Used: 3, CollectedAt: second},
	}); err != nil {
		t.Fatalf("CreateUsageSnapshots() error = %v", err)
	}

	query := url.Values{}
	query.Set("end", "2026-08-06T00:00:00+09:00")
	recorder := httptest.NewRecorder()
	server.mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/activity?"+query.Encode(), nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", recorder.Code, recorder.Body.String())
	}
	var response ActivityResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.UniqueCollections != 2 || response.TotalSnapshots != 3 {
		t.Fatalf("totals = unique %d snapshots %d, want 2 and 3", response.UniqueCollections, response.TotalSnapshots)
	}
	if response.GapCount != 3 || len(response.Gaps) != 3 {
		t.Fatalf("gaps = %#v, want leading/internal/trailing gaps", response.Gaps)
	}
	var gap ActivityGap
	for _, candidate := range response.Gaps {
		if candidate.From.Equal(first.UTC().Add(15 * time.Minute)) {
			gap = candidate
			break
		}
	}
	if gap.From.IsZero() || !gap.To.Equal(second.UTC()) {
		t.Fatalf("internal gap bounds = %s..%s, want %s..%s", gap.From, gap.To, first.UTC().Add(15*time.Minute), second.UTC())
	}
	if gap.DurationSeconds != 2700 || gap.MissingIntervals != 3 {
		t.Fatalf("internal gap duration/missing = %d/%d, want 2700/3", gap.DurationSeconds, gap.MissingIntervals)
	}
}

func TestActivityEndpointReportsFullRangeAndBoundaryGaps(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Seoul")
	windowStart := time.Date(2026, 7, 23, 0, 0, 0, 0, loc)
	windowEnd := time.Date(2026, 8, 6, 0, 0, 0, 0, loc)
	interval := 15 * time.Minute
	expectedIntervals := int(14 * 24 * time.Hour / interval)

	t.Run("empty full range", func(t *testing.T) {
		server, _ := newTempActivityServer(t)
		server.SetCollectionInterval(interval)
		query := url.Values{"end": {windowEnd.Format(time.RFC3339)}}
		recorder := httptest.NewRecorder()
		server.mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/activity?"+query.Encode(), nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d; body = %s", recorder.Code, recorder.Body.String())
		}
		var response ActivityResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode = %v", err)
		}
		if response.GapCount != 1 || len(response.Gaps) != 1 {
			t.Fatalf("gaps = %#v, want one full-range gap", response.Gaps)
		}
		gap := response.Gaps[0]
		if !gap.From.Equal(windowStart.UTC()) || !gap.To.Equal(windowEnd.UTC()) || gap.DurationSeconds != int64(windowEnd.Sub(windowStart)/time.Second) || gap.MissingIntervals != expectedIntervals {
			t.Fatalf("full gap = %#v, want %s..%s duration %d missing %d", gap, windowStart.UTC(), windowEnd.UTC(), windowEnd.Sub(windowStart)/time.Second, expectedIntervals)
		}
	})

	t.Run("one sample at start boundary", func(t *testing.T) {
		server, db := newTempActivityServer(t)
		server.SetCollectionInterval(interval)
		providerID, err := db.CreateProvider("boundary", `{}`)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.CreateUsageSnapshot(&store.UsageSnapshot{ProviderID: providerID, Metric: "session", Used: 1, CollectedAt: windowStart}); err != nil {
			t.Fatal(err)
		}
		query := url.Values{"end": {windowEnd.Format(time.RFC3339)}}
		recorder := httptest.NewRecorder()
		server.mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/activity?"+query.Encode(), nil))
		var response ActivityResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.GapCount != 1 {
			t.Fatalf("gaps = %#v, want trailing gap only", response.Gaps)
		}
		gap := response.Gaps[0]
		wantFrom := windowStart.Add(interval).UTC()
		if !gap.From.Equal(wantFrom) || !gap.To.Equal(windowEnd.UTC()) || gap.MissingIntervals != expectedIntervals-1 {
			t.Fatalf("trailing gap = %#v, want from %s to %s missing %d", gap, wantFrom, windowEnd.UTC(), expectedIntervals-1)
		}
	})
}

func TestMetricSelectedTrendForecastAndProviderMetadataAreTypedAndSafe(t *testing.T) {
	server, db := newTempActivityServer(t)
	providerID, err := db.CreateProvider("claude", `{"auth_method":"oauth_file","cred_source":"/private/token","base_url":"https://private.example","api_key":"private-secret"}`)
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}
	if err := db.EnableProvider(providerID, true); err != nil {
		t.Fatalf("EnableProvider() error = %v", err)
	}
	rawError := "private raw collector error"
	if err := db.UpdateProviderStatus(providerID, &rawError); err != nil {
		t.Fatalf("UpdateProviderStatus() error = %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	sessionLimit, weeklyLimit := 100.0, 200.0
	reset := now.Add(6 * time.Hour)
	if err := db.CreateUsageSnapshots([]*store.UsageSnapshot{
		{ProviderID: providerID, Metric: "session", Used: 20, Limit: &sessionLimit, ResetAt: &reset, CollectedAt: now.Add(-time.Hour)},
		{ProviderID: providerID, Metric: "session", Used: 40, Limit: &sessionLimit, ResetAt: &reset, CollectedAt: now},
		{ProviderID: providerID, Metric: "weekly", Used: 60, Limit: &weeklyLimit, ResetAt: &reset, CollectedAt: now.Add(-time.Hour)},
		{ProviderID: providerID, Metric: "weekly", Used: 80, Limit: &weeklyLimit, ResetAt: &reset, CollectedAt: now},
	}); err != nil {
		t.Fatalf("CreateUsageSnapshots() error = %v", err)
	}

	recorder := httptest.NewRecorder()
	server.mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/trends?provider_id=claude&metric=weekly&view=current&bucket=hour", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("trends status = %d; body = %s", recorder.Code, recorder.Body.String())
	}
	var trends map[string]interface{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &trends); err != nil {
		t.Fatalf("decode trends = %v", err)
	}
	if trends["metric"] != "weekly" || trends["cycle_type"] != "weekly" {
		t.Fatalf("selected trend metadata = %#v", trends)
	}
	data := trends["data"].([]interface{})
	for _, point := range data {
		if point.(map[string]interface{})["metric"] != "weekly" {
			t.Fatalf("selected trend leaked another metric: %#v", point)
		}
	}

	recorder = httptest.NewRecorder()
	server.mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/forecast?provider_id=claude", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("forecast status = %d; body = %s", recorder.Code, recorder.Body.String())
	}
	var forecast map[string]interface{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &forecast); err != nil {
		t.Fatalf("decode forecast = %v", err)
	}
	if _, ok := forecast["limit_value"].(float64); !ok {
		t.Fatalf("provider forecast limit_value = %#v, want number", forecast["limit_value"])
	}
	metricForecast := forecast["metrics"].(map[string]interface{})["weekly"].(map[string]interface{})
	if got, ok := metricForecast["limit_value"].(float64); !ok || got != weeklyLimit {
		t.Fatalf("weekly forecast limit_value = %#v, want %v", metricForecast["limit_value"], weeklyLimit)
	}
	for _, secret := range []string{rawError, "/private/token", "private.example", "private-secret"} {
		if strings.Contains(recorder.Body.String(), secret) {
			t.Fatalf("forecast response leaked %q: %s", secret, recorder.Body.String())
		}
	}

	recorder = httptest.NewRecorder()
	server.mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/current", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("current status = %d; body = %s", recorder.Code, recorder.Body.String())
	}
	for _, secret := range []string{rawError, "/private/token", "private.example", "private-secret"} {
		if strings.Contains(recorder.Body.String(), secret) {
			t.Fatalf("current response leaked %q: %s", secret, recorder.Body.String())
		}
	}

	recorder = httptest.NewRecorder()
	server.mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/providers", nil))
	body := recorder.Body.String()
	for _, secret := range []string{"/private/token", "private.example", "private-secret", rawError, "config_json"} {
		if strings.Contains(body, secret) {
			t.Fatalf("provider metadata leaked %q: %s", secret, body)
		}
	}
	if !strings.Contains(body, `"auth_method":"oauth_file"`) {
		t.Fatalf("provider metadata omitted safe auth method: %s", body)
	}

	providers, err := db.ListProviders()
	if err != nil {
		t.Fatalf("ListProviders() error = %v", err)
	}
	legacy := domain.ProviderView{ID: providers[0].ID, Name: providers[0].Name, Enabled: true}
	ssr := server.buildSSRProviderView(legacy, now)
	if len(ssr.Metrics) != 2 || ssr.Metrics[0].CycleType == "" || !ssr.Metrics[0].HasProjection {
		t.Fatalf("SSR metrics = %#v, want per-metric projections", ssr.Metrics)
	}
	recorder = httptest.NewRecorder()
	server.mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("dashboard status = %d; body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `data-metric="weekly"`) || !strings.Contains(recorder.Body.String(), `data-percent="40`) {
		t.Fatalf("dashboard omitted projection-rich SSR metric output: %s", recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), rawError) {
		t.Fatalf("dashboard leaked raw provider error: %s", recorder.Body.String())
	}
}

func TestProviderTrendRateModeReturnsResetAwareConsecutiveDeltas(t *testing.T) {
	server, db := newTempActivityServer(t)
	providerID, err := db.CreateProvider("claude", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	values := []float64{10, 15, 5, 8}
	for index, value := range values {
		if _, err := db.CreateUsageSnapshot(&store.UsageSnapshot{ProviderID: providerID, Metric: "session", Used: value, CollectedAt: now.Add(time.Duration(index-3) * time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	recorder := httptest.NewRecorder()
	server.mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/trends?provider_id=claude&metric=session&view=current&mode=rate&bucket=none", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data []struct {
			Value float64 `json:"value"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	want := []float64{0, 5, 0, 3}
	if len(response.Data) != len(want) {
		t.Fatalf("rate points = %d, want %d; body=%s", len(response.Data), len(want), recorder.Body.String())
	}
	for index, point := range response.Data {
		if point.Value != want[index] {
			t.Errorf("rate[%d] = %v, want %v", index, point.Value, want[index])
		}
	}
}

func TestCompositionRootWiresConfiguredCollectionIntervalToRealActivityEndpoint(t *testing.T) {
	if testing.Short() {
		t.Skip("composition-root process test is disabled in short mode")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("repo root = %v", err)
	}
	dbPath := filepath.Join(t.TempDir(), "usage.db")
	seed, err := store.NewStore(dbPath)
	if err != nil {
		t.Fatalf("seed store = %v", err)
	}
	providerID, err := seed.CreateProvider("composition", `{}`)
	if err != nil {
		seed.Close()
		t.Fatalf("seed provider = %v", err)
	}
	if _, err := seed.CreateUsageSnapshot(&store.UsageSnapshot{ProviderID: providerID, Metric: "session", Used: 1, CollectedAt: time.Now().UTC()}); err != nil {
		seed.Close()
		t.Fatalf("seed snapshot = %v", err)
	}
	if err := seed.Close(); err != nil {
		t.Fatalf("close seed store = %v", err)
	}

	portListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port = %v", err)
	}
	port := portListener.Addr().(*net.TCPAddr).Port
	_ = portListener.Close()
	binaryPath := filepath.Join(t.TempDir(), "webusage-server")
	build := exec.Command("mise", "exec", "--", "go", "build", "-o", binaryPath, "./cmd/server")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build server: %v\n%s", err, output)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, binaryPath)
	cmd.Dir = root
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	env := make([]string, 0, len(os.Environ()))
	for _, item := range os.Environ() {
		key := strings.SplitN(item, "=", 2)[0]
		switch key {
		case "DB_PATH", "SERVER_HOST", "SERVER_PORT", "COLLECTION_INTERVAL", "OPENUSAGE_ENABLED", "OPENUSAGE_URL", "OLLAMA_API_KEY", "TITLE", "HOME":
			continue
		}
		env = append(env, item)
	}
	env = append(env,
		"DB_PATH="+dbPath,
		"SERVER_HOST=127.0.0.1",
		"SERVER_PORT="+strconv.Itoa(port),
		"COLLECTION_INTERVAL=1234",
		"OPENUSAGE_ENABLED=false",
		"OPENUSAGE_URL=http://127.0.0.1:1",
		"HOME="+t.TempDir(),
	)
	cmd.Env = env
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server = %v", err)
	}
	defer func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		wait := make(chan error, 1)
		go func() { wait <- cmd.Wait() }()
		select {
		case <-wait:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-wait
		}
	}()

	client := &http.Client{Timeout: 500 * time.Millisecond}
	endpoint := fmt.Sprintf("http://127.0.0.1:%d/api/activity", port)
	var response ActivityResponse
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		res, requestErr := client.Get(endpoint)
		if requestErr == nil {
			payload, readErr := io.ReadAll(res.Body)
			res.Body.Close()
			if readErr == nil && res.StatusCode == http.StatusOK && json.Unmarshal(payload, &response) == nil {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if response.CollectionIntervalSeconds != 1234 {
		t.Fatalf("real /api/activity interval = %d, want 1234", response.CollectionIntervalSeconds)
	}
}
