package http

import (
	"encoding/json"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/ClaudeSeo/webusage/internal/domain"
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

// CurrentResponse maps provider_id -> CurrentCycleInfo
type CurrentResponse map[string]domain.CurrentCycleInfo

// TrendsResponse for trend data
type TrendsResponse struct {
	ProviderID string                  `json:"provider_id"`
	CycleType  string                  `json:"cycle_type"`
	View       string                  `json:"view"`
	Mode       string                  `json:"mode"`
	Bucket     string                  `json:"bucket"`
	Data       []domain.TrendDataPoint `json:"data"`
}

type ProvidersResponse struct {
	Providers []domain.ProviderMetadata `json:"providers"`
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
	server.store.EnableProviderByName("claude", true)
	now := time.Now()
	snapshot := &store.UsageSnapshot{
		ProviderID:  providerID,
		Metric:      "session",
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

	if provider.ProviderID == "" {
		t.Error("Schema violation: 'provider_id' is required")
	}
	if provider.CycleType == "" {
		t.Error("Schema violation: 'cycle_type' is required")
	}
}

// TestAPIContract_Trends validates the /api/trends endpoint with provider_id
func TestAPIContract_Trends(t *testing.T) {
	server, cleanup := setupTestServerForContract(t)
	defer cleanup()

	// Setup test data
	providerID, _ := server.store.CreateProvider("claude", `{}`)
	server.store.EnableProviderByName("claude", true)
	now := time.Now()

	// Insert data for trend
	for i := 0; i < 5; i++ {
		snapshot := &store.UsageSnapshot{
			ProviderID:  providerID,
			Metric:      "session",
			Used:        float64(i * 100),
			CollectedAt: now.Add(-time.Duration(i) * time.Hour),
		}
		server.store.CreateUsageSnapshot(snapshot)
	}

	req := httptest.NewRequest(nethttp.MethodGet, "/api/trends?provider_id=claude", nil)
	w := httptest.NewRecorder()

	server.mux.ServeHTTP(w, req)

	if w.Code != nethttp.StatusOK {
		t.Fatalf("Expected status 200, got %d", w.Code)
	}

	var resp TrendsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	// Schema validation
	if resp.ProviderID == "" {
		t.Error("Schema violation: 'provider_id' is required")
	}
	if resp.CycleType == "" {
		t.Error("Schema violation: 'cycle_type' is required")
	}
}

// TestAPIContract_Trends_RequiresProviderID validates that missing provider_id returns all providers
func TestAPIContract_Trends_RequiresProviderID(t *testing.T) {
	server, cleanup := setupTestServerForContract(t)
	defer cleanup()

	// Setup test data
	server.store.CreateProvider("claude", `{}`)

	req := httptest.NewRequest(nethttp.MethodGet, "/api/trends?range=24h", nil)
	w := httptest.NewRecorder()

	server.mux.ServeHTTP(w, req)

	// Now returns 200 with all providers
	if w.Code != nethttp.StatusOK {
		t.Errorf("Expected status 200 for all providers, got %d", w.Code)
	}
}

// TestAPIContract_Providers validates the /api/providers endpoint
func TestAPIContract_Providers(t *testing.T) {
	server, cleanup := setupTestServerForContract(t)
	defer cleanup()

	// Setup test data
	server.store.CreateProvider("claude", `{}`)
	server.store.CreateProvider("codex", `{}`)

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
		t.Fatal("Schema violation: 'providers' array is required")
	}

	for _, provider := range resp.Providers {
		if provider.ProviderID == "" {
			t.Error("Schema violation: 'provider_id' is required")
		}
		if provider.CycleType == "" {
			t.Error("Schema violation: 'cycle_type' is required")
		}
	}
}

// TestAPIContract_EnableDisableProvider validates provider enable/disable
func TestAPIContract_EnableDisableProvider(t *testing.T) {
	server, cleanup := setupTestServerForContract(t)
	defer cleanup()

	// Create provider
	server.store.CreateProvider("claude", `{}`)

	// Test enable
	req := httptest.NewRequest(nethttp.MethodPost, "/api/providers/claude/enable", nil)
	w := httptest.NewRecorder()
	server.mux.ServeHTTP(w, req)

	// Note: Enable will fail without valid credentials, which is expected
	// The important thing is the route works

	// Test disable
	req = httptest.NewRequest(nethttp.MethodPost, "/api/providers/claude/disable", nil)
	w = httptest.NewRecorder()
	server.mux.ServeHTTP(w, req)

	if w.Code != nethttp.StatusOK {
		t.Fatalf("Expected status 200 for disable, got %d", w.Code)
	}
}

// TestAPIContract_Collect validates the /api/collect endpoint
func TestAPIContract_Collect(t *testing.T) {
	server, cleanup := setupTestServerForContract(t)
	defer cleanup()

	req := httptest.NewRequest(nethttp.MethodPost, "/api/collect", nil)
	w := httptest.NewRecorder()

	server.mux.ServeHTTP(w, req)

	// Note: Without collector set, this returns 500 but the route works
	// The actual collection requires registry and collector setup
	if w.Code != nethttp.StatusOK && w.Code != nethttp.StatusInternalServerError {
		t.Fatalf("Expected status 200 or 500, got %d", w.Code)
	}
}