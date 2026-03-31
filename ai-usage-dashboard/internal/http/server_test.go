package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/openclaw/ai-usage-dashboard/internal/store"
	"log/slog"
)

func setupTestServer(t *testing.T) (*Server, func()) {
	// Create temp database
	tmpFile := "/tmp/test_http_" + time.Now().Format("20060102150405") + ".db"
	
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	}))
	
	s, err := store.NewStore(tmpFile)
	if err != nil {
		t.Fatalf("Failed to create test store: %v", err)
	}
	
	server, err := NewServer(s, 8080, logger)
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

func TestHealthzEndpoint(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()
	
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	
	server.mux.ServeHTTP(w, req)
	
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}
	
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}
	
	if resp["status"] != "healthy" {
		t.Errorf("Expected status 'healthy', got '%v'", resp["status"])
	}
}

func TestProvidersEndpoint(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()
	
	// Add a test provider
	_, err := server.store.CreateProvider("test-provider", `{"api_key":"test"}`)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}
	
	req := httptest.NewRequest(http.MethodGet, "/api/providers", nil)
	w := httptest.NewRecorder()
	
	server.mux.ServeHTTP(w, req)
	
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}
	
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}
	
	providers, ok := resp["providers"].([]interface{})
	if !ok {
		t.Fatal("Expected providers array")
	}
	
	if len(providers) != 1 {
		t.Errorf("Expected 1 provider, got %d", len(providers))
	}
}

func TestCurrentUsageEndpoint(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()
	
	// Add provider and usage data
	providerID, _ := server.store.CreateProvider("test-provider", `{}`)
	now := time.Now()
	
	snapshot := &store.UsageSnapshot{
		ProviderID:  providerID,
		Metric:      "tokens",
		Used:        5000.0,
		CollectedAt: now,
		RawJSON:     `{"test":true}`,
	}
	server.store.CreateUsageSnapshot(snapshot)
	
	req := httptest.NewRequest(http.MethodGet, "/api/current", nil)
	w := httptest.NewRecorder()
	
	server.mux.ServeHTTP(w, req)
	
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}
	
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}
	
	testProvider, ok := resp["test-provider"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected test-provider in response")
	}
	
	metrics, ok := testProvider["metrics"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected metrics object")
	}
	
	if metrics["tokens"] != 5000.0 {
		t.Errorf("Expected tokens 5000.0, got %v", metrics["tokens"])
	}
}

func TestTrendsEndpoint(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()
	
	// Add provider and historical data
	providerID, _ := server.store.CreateProvider("test-provider", `{}`)
	now := time.Now()
	
	snapshots := []*store.UsageSnapshot{
		{ProviderID: providerID, Metric: "tokens", Used: 1000, CollectedAt: now.Add(-2 * time.Hour)},
		{ProviderID: providerID, Metric: "tokens", Used: 2000, CollectedAt: now.Add(-time.Hour)},
		{ProviderID: providerID, Metric: "tokens", Used: 3000, CollectedAt: now},
	}
	server.store.CreateUsageSnapshots(snapshots)
	
	req := httptest.NewRequest(http.MethodGet, "/api/trends?range=24h", nil)
	w := httptest.NewRecorder()
	
	server.mux.ServeHTTP(w, req)
	
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}
	
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}
	
	testProvider, ok := resp["test-provider"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected test-provider in response")
	}
	
	trend, ok := testProvider["trend"].([]interface{})
	if !ok {
		t.Fatal("Expected trend array")
	}
	
	if len(trend) != 3 {
		t.Errorf("Expected 3 trend points, got %d", len(trend))
	}
}

func TestDashboardEndpoint(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()
	
	// Add provider
	providerID, _ := server.store.CreateProvider("test-provider", `{}`)
	now := time.Now()
	
	snapshot := &store.UsageSnapshot{
		ProviderID:  providerID,
		Metric:      "tokens",
		Used:        5000.0,
		CollectedAt: now,
	}
	server.store.CreateUsageSnapshot(snapshot)
	
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	
	server.mux.ServeHTTP(w, req)
	
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}
	
	body := w.Body.String()
	if !contains(body, "AI Usage Dashboard") {
		t.Error("Expected dashboard title in response")
	}
	if !contains(body, "test-provider") {
		t.Error("Expected provider name in response")
	}
}

func TestMethodNotAllowed(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()
	
	req := httptest.NewRequest(http.MethodPost, "/api/current", nil)
	w := httptest.NewRecorder()
	
	server.mux.ServeHTTP(w, req)
	
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status 405, got %d", w.Code)
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && 
		(len(s) >= len(substr) && (s == substr || len(s) > len(substr) && 
		(findSubstring(s, substr))))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
