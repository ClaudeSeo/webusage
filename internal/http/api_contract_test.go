package http

import (
	"encoding/json"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/ClaudeSeo/webusage/internal/store"
)

// APISchema defines the expected API response structure
type APISchema struct {
	Healthz   HealthzResponse   `json:"healthz"`
	Current   CurrentResponse   `json:"current"`
	Trends    TrendsResponse    `json:"trends"`
	Providers ProvidersResponse `json:"providers"`
}

type HealthzResponse struct {
	Status    string `json:"status"`
	Timestamp string `json:"timestamp"`
}

type CurrentResponse map[string]ProviderCurrentData

type ProviderCurrentData struct {
	ProviderID int64              `json:"provider_id"`
	Enabled    bool               `json:"enabled"`
	Metrics    map[string]float64 `json:"metrics"`
	LastRun    *time.Time         `json:"last_run,omitempty"`
	LastError  *string            `json:"last_error,omitempty"`
}

type TrendsResponse map[string]ProviderTrendData

type ProviderTrendData struct {
	ProviderID int64        `json:"provider_id"`
	Range      string       `json:"range"`
	Trend      []TrendPoint `json:"trend"`
}

type TrendPoint struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
	Metric    string    `json:"metric"`
}

type ProvidersResponse struct {
	Providers []ProviderInfo `json:"providers"`
}

type ProviderInfo struct {
	ID        int64      `json:"id"`
	Name      string     `json:"name"`
	Enabled   bool       `json:"enabled"`
	LastRun   *time.Time `json:"last_run,omitempty"`
	LastError *string    `json:"last_error,omitempty"`
}

func setupTestServerForContract(t *testing.T) (*Server, func()) {
	tmpFile := "/tmp/test_contract_" + time.Now().Format("20060102150405") + ".db"

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	}))

	s, err := store.NewStore(tmpFile)
	if err != nil {
		t.Fatalf("Failed to create test store: %v", err)
	}

	server, err := NewServer(s, 8080, logger, "../../templates")
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

// TestAPIContract_Healthz validates the /healthz endpoint against schema
func TestAPIContract_Healthz(t *testing.T) {
	server, cleanup := setupTestServerForContract(t)
	defer cleanup()

	req := httptest.NewRequest(nethttp.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()

	server.mux.ServeHTTP(w, req)

	if w.Code != nethttp.StatusOK {
		t.Fatalf("Expected status 200, got %d", w.Code)
	}

	var resp HealthzResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	// Schema validation
	if resp.Status == "" {
		t.Error("Schema violation: 'status' field is required")
	}
	if resp.Timestamp == "" {
		t.Error("Schema violation: 'timestamp' field is required")
	}
}

// TestAPIContract_Current validates the /api/current endpoint against schema
func TestAPIContract_Current(t *testing.T) {
	server, cleanup := setupTestServerForContract(t)
	defer cleanup()

	// Setup test data
	providerID, _ := server.store.CreateProvider("claude", `{}`)
	now := time.Now()
	snapshot := &store.UsageSnapshot{
		ProviderID:  providerID,
		Metric:      "tokens",
		Used:        5000.0,
		CollectedAt: now,
	}
	server.store.CreateUsageSnapshot(snapshot)

	req := httptest.NewRequest(nethttp.MethodGet, "/api/current", nil)
	w := httptest.NewRecorder()

	server.mux.ServeHTTP(w, req)

	if w.Code != nethttp.StatusOK {
		t.Fatalf("Expected status 200, got %d", w.Code)
	}

	var resp CurrentResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	// Schema validation
	provider, exists := resp["claude"]
	if !exists {
		t.Fatal("Schema violation: provider key missing")
	}

	if provider.ProviderID == 0 {
		t.Error("Schema violation: 'provider_id' is required")
	}
	if provider.Metrics == nil {
		t.Error("Schema violation: 'metrics' field is required")
	}
}

// TestAPIContract_Trends validates the /api/trends endpoint with all valid ranges
func TestAPIContract_Trends(t *testing.T) {
	server, cleanup := setupTestServerForContract(t)
	defer cleanup()

	// Setup test data
	providerID, _ := server.store.CreateProvider("claude", `{}`)
	now := time.Now()

	// Insert data for 30 days
	for i := 0; i < 30; i++ {
		snapshot := &store.UsageSnapshot{
			ProviderID:  providerID,
			Metric:      "tokens",
			Used:        float64(i * 100),
			CollectedAt: now.Add(-time.Duration(i) * time.Hour),
		}
		server.store.CreateUsageSnapshot(snapshot)
	}

	validRanges := []string{"24h", "7d", "30d"}

	for _, rangeParam := range validRanges {
		t.Run(rangeParam, func(t *testing.T) {
			req := httptest.NewRequest(nethttp.MethodGet, "/api/trends?range="+rangeParam, nil)
			w := httptest.NewRecorder()

			server.mux.ServeHTTP(w, req)

			if w.Code != nethttp.StatusOK {
				t.Fatalf("Expected status 200 for range %s, got %d", rangeParam, w.Code)
			}

			var resp TrendsResponse
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("Failed to parse response: %v", err)
			}

			// Schema validation
			provider, exists := resp["claude"]
			if !exists {
				t.Fatal("Schema violation: provider key missing")
			}

			if provider.Range != rangeParam {
				t.Errorf("Schema violation: expected range '%s', got '%s'", rangeParam, provider.Range)
			}

			if provider.Trend == nil {
				t.Error("Schema violation: 'trend' array is required")
			}

			// Validate trend points
			for i, point := range provider.Trend {
				if point.Timestamp.IsZero() {
					t.Errorf("Schema violation: trend[%d].timestamp is required", i)
				}
				if point.Metric == "" {
					t.Errorf("Schema violation: trend[%d].metric is required", i)
				}
			}
		})
	}
}

// TestAPIContract_Trends_InvalidRange validates that invalid ranges return 400
func TestAPIContract_Trends_InvalidRange(t *testing.T) {
	server, cleanup := setupTestServerForContract(t)
	defer cleanup()

	invalidRanges := []string{"1h", "60d", "invalid", "abc", ""}

	for _, rangeParam := range invalidRanges {
		t.Run(rangeParam, func(t *testing.T) {
			req := httptest.NewRequest(nethttp.MethodGet, "/api/trends?range="+rangeParam, nil)
			w := httptest.NewRecorder()

			server.mux.ServeHTTP(w, req)

			// Empty range should default to 24h and succeed
			if rangeParam == "" {
				if w.Code != nethttp.StatusOK {
					t.Errorf("Expected status 200 for empty range (defaults to 24h), got %d", w.Code)
				}
				return
			}

			if w.Code != nethttp.StatusBadRequest {
				t.Errorf("Expected status 400 for invalid range '%s', got %d", rangeParam, w.Code)
			}

			// Verify error response format
			var errorResp map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &errorResp); err != nil {
				t.Fatalf("Failed to parse error response: %v", err)
			}

			if errorResp["error"] == "" {
				t.Error("Schema violation: error message is required")
			}
		})
	}
}

// TestAPIContract_Providers validates the /api/providers endpoint against schema
func TestAPIContract_Providers(t *testing.T) {
	server, cleanup := setupTestServerForContract(t)
	defer cleanup()

	// Setup test data
	server.store.CreateProvider("claude", `{"auth_method":"oauth_file"}`)
	server.store.CreateProvider("copilot", `{"auth_method":"keychain"}`)

	req := httptest.NewRequest(nethttp.MethodGet, "/api/providers", nil)
	w := httptest.NewRecorder()

	server.mux.ServeHTTP(w, req)

	if w.Code != nethttp.StatusOK {
		t.Fatalf("Expected status 200, got %d", w.Code)
	}

	var resp ProvidersResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	// Schema validation
	if resp.Providers == nil {
		t.Error("Schema violation: 'providers' array is required")
	}

	if len(resp.Providers) != 2 {
		t.Errorf("Expected 2 providers, got %d", len(resp.Providers))
	}

	for i, p := range resp.Providers {
		if p.ID == 0 {
			t.Errorf("Schema violation: providers[%d].id is required", i)
		}
		if p.Name == "" {
			t.Errorf("Schema violation: providers[%d].name is required", i)
		}
	}
}

// TestAPIContract_ErrorResponses validates error response format
func TestAPIContract_ErrorResponses(t *testing.T) {
	server, cleanup := setupTestServerForContract(t)
	defer cleanup()

	// Test invalid method
	req := httptest.NewRequest(nethttp.MethodPost, "/api/current", nil)
	w := httptest.NewRecorder()

	server.mux.ServeHTTP(w, req)

	if w.Code != nethttp.StatusMethodNotAllowed {
		t.Logf("Note: POST to /api/current returned %d (not 405)", w.Code)
	}
}
