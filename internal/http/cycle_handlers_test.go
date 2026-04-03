package http

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/ClaudeSeo/webusage/internal/domain"
	"github.com/ClaudeSeo/webusage/internal/store"
)

// setupTestServerForCycle creates a test server with cycle handlers
func setupTestServerForCycle(t *testing.T) (*Server, func()) {
	tmpFile := "/tmp/test_cycle_" + time.Now().Format("20060102150405") + ".db"

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	}))

	s, err := store.NewStore(tmpFile)
	if err != nil {
		t.Fatalf("Failed to create test store: %v", err)
	}

	server, err := NewServer(s, "127.0.0.1", 8080, logger, "../../templates")
	if err != nil {
		t.Fatalf("Failed to create test server: %v", err)
	}

	cleanup := func() {
		s.Close()
		os.Remove(tmpFile)
		os.Remove(tmpFile + "-wal")
		os.Remove(tmpFile + "-shm")
	}

	return server, cleanup
}

// TestCycleAware_Current validates the /api/current endpoint
func TestCycleAware_Current(t *testing.T) {
	server, cleanup := setupTestServerForCycle(t)
	defer cleanup()

	// Setup test providers
	claudeID, _ := server.store.CreateProvider("claude", `{}`)
	copilotID, _ := server.store.CreateProvider("copilot", `{}`)
	now := time.Now()

	// Insert test usage data for claude (rolling_5h cycle)
	claudeSnapshot := &store.UsageSnapshot{
		ProviderID:  claudeID,
		Metric:      "session",
		Used:        45.0,
		Limit:       floatPtr(100.0),
		ResetAt:     timePtr(now.Add(2 * time.Hour)),
		CollectedAt: now,
	}
	server.store.CreateUsageSnapshot(claudeSnapshot)

	// Insert test usage data for copilot (monthly cycle)
	copilotSnapshot := &store.UsageSnapshot{
		ProviderID:  copilotID,
		Metric:      "premium_interactions",
		Used:        500.0,
		Limit:       floatPtr(1000.0),
		ResetAt:     timePtr(now.Add(30 * 24 * time.Hour)),
		CollectedAt: now,
	}
	server.store.CreateUsageSnapshot(copilotSnapshot)

	// Test the endpoint
	req := httptest.NewRequest(http.MethodGet, "/api/current", nil)
	w := httptest.NewRecorder()

	server.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", w.Code)
	}

	var resp map[string]domain.CurrentCycleInfo
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	// Validate claude response
	claudeInfo, ok := resp["claude"]
	if !ok {
		t.Fatal("Expected 'claude' in response")
	}
	if claudeInfo.CycleType != "rolling_5h" {
		t.Errorf("Expected cycle_type 'rolling_5h' for claude, got '%s'", claudeInfo.CycleType)
	}
	if claudeInfo.CurrentUsage != 45.0 {
		t.Errorf("Expected current_usage 45.0 for claude, got %f", claudeInfo.CurrentUsage)
	}
	if claudeInfo.UsagePercent != 45.0 {
		t.Errorf("Expected usage_percent 45.0 for claude, got %f", claudeInfo.UsagePercent)
	}

	// Validate copilot response
	copilotInfo, ok := resp["copilot"]
	if !ok {
		t.Fatal("Expected 'copilot' in response")
	}
	if copilotInfo.CycleType != "monthly" {
		t.Errorf("Expected cycle_type 'monthly' for copilot, got '%s'", copilotInfo.CycleType)
	}
	if copilotInfo.CurrentUsage != 500.0 {
		t.Errorf("Expected current_usage 500.0 for copilot, got %f", copilotInfo.CurrentUsage)
	}
}

// TestCycleAware_Trends validates the /api/trends endpoint
func TestCycleAware_Trends(t *testing.T) {
	server, cleanup := setupTestServerForCycle(t)
	defer cleanup()

	// Setup test provider
	claudeID, _ := server.store.CreateProvider("claude", `{}`)
	now := time.Now()

	// Insert trend data
	for i := 0; i < 5; i++ {
		snapshot := &store.UsageSnapshot{
			ProviderID:  claudeID,
			Metric:      "session",
			Used:        float64(i * 10),
			CollectedAt: now.Add(-time.Duration(5-i) * time.Hour),
		}
		server.store.CreateUsageSnapshot(snapshot)
	}

	testCases := []struct {
		name       string
		view       string
		mode       string
		bucket     string
		expectCode int
	}{
		{"current view", "current", "absolute", "auto", http.StatusOK},
		{"previous view", "previous", "absolute", "auto", http.StatusOK},
		{"both view", "both", "absolute", "auto", http.StatusOK},
		{"relative mode", "current", "relative", "auto", http.StatusOK},
		{"hour bucket", "current", "absolute", "hour", http.StatusOK},
		{"day bucket", "current", "absolute", "day", http.StatusOK},
		{"invalid view defaults", "invalid", "absolute", "auto", http.StatusOK},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			url := "/api/trends?provider_id=claude&view=" + tc.view + "&mode=" + tc.mode + "&bucket=" + tc.bucket
			req := httptest.NewRequest(http.MethodGet, url, nil)
			w := httptest.NewRecorder()

			server.mux.ServeHTTP(w, req)

			if w.Code != tc.expectCode {
				t.Errorf("Expected status %d, got %d", tc.expectCode, w.Code)
			}

			if w.Code == http.StatusOK {
				var resp domain.ProviderTrends
				if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
					t.Fatalf("Failed to parse response: %v", err)
				}

				if resp.ProviderID != "claude" {
					t.Errorf("Expected provider_id 'claude', got '%s'", resp.ProviderID)
				}
				if resp.CycleType != "rolling_5h" {
					t.Errorf("Expected cycle_type 'rolling_5h', got '%s'", resp.CycleType)
				}
				if len(resp.Data) == 0 {
					t.Error("Expected non-empty trend data")
				}
			}
		})
	}
}

// TestCycleAware_Trends_MissingProvider tests error handling for missing provider
func TestCycleAware_Trends_MissingProvider(t *testing.T) {
	server, cleanup := setupTestServerForCycle(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/trends?provider_id=nonexistent", nil)
	w := httptest.NewRecorder()

	server.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", w.Code)
	}
}

// TestCycleAware_Trends_NoProviderID tests that missing provider_id returns all providers
func TestCycleAware_Trends_NoProviderID(t *testing.T) {
	server, cleanup := setupTestServerForCycle(t)
	defer cleanup()

	// Setup test provider
	claudeID, _ := server.store.CreateProvider("claude", `{}`)
	snapshot := &store.UsageSnapshot{
		ProviderID:  claudeID,
		Metric:      "session",
		Used:        50.0,
		Limit:       floatPtr(100.0),
		CollectedAt: time.Now(),
	}
	server.store.CreateUsageSnapshot(snapshot)

	req := httptest.NewRequest(http.MethodGet, "/api/trends?range=24h", nil)
	w := httptest.NewRecorder()

	server.mux.ServeHTTP(w, req)

	// Now returns 200 with all providers
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	// Should have claude provider
	if _, ok := resp["claude"]; !ok {
		t.Error("Expected 'claude' in response")
	}
}

// TestCycleAware_Forecast validates the /api/forecast endpoint
func TestCycleAware_Forecast(t *testing.T) {
	server, cleanup := setupTestServerForCycle(t)
	defer cleanup()

	// Setup test provider with trend data
	claudeID, _ := server.store.CreateProvider("claude", `{}`)
	now := time.Now()

	// Insert trend data for forecasting
	for i := 0; i < 10; i++ {
		snapshot := &store.UsageSnapshot{
			ProviderID:  claudeID,
			Metric:      "session",
			Used:        float64(20 + i*5),
			Limit:       floatPtr(100.0),
			ResetAt:     timePtr(now.Add(5 * time.Hour)),
			CollectedAt: now.Add(-time.Duration(10-i) * time.Hour),
		}
		server.store.CreateUsageSnapshot(snapshot)
	}

	// Test all providers forecast
	req := httptest.NewRequest(http.MethodGet, "/api/forecast", nil)
	w := httptest.NewRecorder()

	server.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", w.Code)
	}

	var resp map[string][]domain.ForecastInfo
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	forecasts, ok := resp["forecasts"]
	if !ok {
		t.Fatal("Expected 'forecasts' key in response")
	}
	if len(forecasts) == 0 {
		t.Fatal("Expected non-empty forecasts array")
	}

	// Find claude forecast
	var claudeForecast *domain.ForecastInfo
	for i := range forecasts {
		if forecasts[i].ProviderID == "claude" {
			claudeForecast = &forecasts[i]
			break
		}
	}

	if claudeForecast == nil {
		t.Fatal("Expected 'claude' in forecasts")
	}

	if claudeForecast.CycleType != "rolling_5h" {
		t.Errorf("Expected cycle_type 'rolling_5h', got '%s'", claudeForecast.CycleType)
	}
	if claudeForecast.CurrentUsage != 65.0 {
		t.Errorf("Expected current_usage 65.0 (last value), got %f", claudeForecast.CurrentUsage)
	}
	if claudeForecast.Confidence <= 0 {
		t.Error("Expected positive confidence value")
	}
}

// TestCycleAware_Forecast_SingleProvider tests forecast for specific provider
func TestCycleAware_Forecast_SingleProvider(t *testing.T) {
	server, cleanup := setupTestServerForCycle(t)
	defer cleanup()

	// Setup test provider
	claudeID, _ := server.store.CreateProvider("claude", `{}`)
	now := time.Now()

	for i := 0; i < 10; i++ {
		snapshot := &store.UsageSnapshot{
			ProviderID:  claudeID,
			Metric:      "session",
			Used:        float64(20 + i*5),
			Limit:       floatPtr(100.0),
			ResetAt:     timePtr(now.Add(5 * time.Hour)),
			CollectedAt: now.Add(-time.Duration(10-i) * time.Hour),
		}
		server.store.CreateUsageSnapshot(snapshot)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/forecast?provider_id=claude", nil)
	w := httptest.NewRecorder()

	server.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", w.Code)
	}

	var resp domain.ForecastInfo
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if resp.ProviderID != "claude" {
		t.Errorf("Expected provider_id 'claude', got '%s'", resp.ProviderID)
	}
}

// TestCycleAware_ProvidersMeta validates the /api/providers endpoint
func TestCycleAware_ProvidersMeta(t *testing.T) {
	server, cleanup := setupTestServerForCycle(t)
	defer cleanup()

	// Setup test providers
	server.store.CreateProvider("claude", `{"auth_method":"oauth_file"}`)
	server.store.CreateProvider("copilot", `{"auth_method":"keychain"}`)

	req := httptest.NewRequest(http.MethodGet, "/api/providers", nil)
	w := httptest.NewRecorder()

	server.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", w.Code)
	}

	var resp map[string][]domain.ProviderMetadata
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	providers, ok := resp["providers"]
	if !ok {
		t.Fatal("Expected 'providers' key in response")
	}
	if len(providers) != 2 {
		t.Errorf("Expected 2 providers, got %d", len(providers))
	}

	for _, p := range providers {
		if p.ProviderID == "" {
			t.Error("Expected non-empty provider_id")
		}
		if p.CycleType == "" {
			t.Errorf("Expected non-empty cycle_type for provider %s", p.ProviderID)
		}
		if p.LimitType == "" {
			t.Errorf("Expected non-empty limit_type for provider %s", p.ProviderID)
		}
		if len(p.SupportedViews) == 0 {
			t.Errorf("Expected non-empty supported_views for provider %s", p.ProviderID)
		}
	}
}

// TestCycleAware_MethodNotAllowed validates method restrictions
func TestCycleAware_MethodNotAllowed(t *testing.T) {
	server, cleanup := setupTestServerForCycle(t)
	defer cleanup()

	endpoints := []string{
		"/api/current",
		"/api/trends?provider_id=test",
		"/api/forecast",
		"/api/providers",
	}

	for _, endpoint := range endpoints {
		t.Run(endpoint, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, endpoint, nil)
			w := httptest.NewRecorder()

			server.mux.ServeHTTP(w, req)

			if w.Code != http.StatusMethodNotAllowed {
				t.Errorf("Expected status 405 for POST %s, got %d", endpoint, w.Code)
			}
		})
	}
}

// Helper functions
func floatPtr(f float64) *float64 {
	return &f
}

func timePtr(t time.Time) *time.Time {
	return &t
}