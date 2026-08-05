package http

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ClaudeSeo/webusage/internal/collector"
	"github.com/ClaudeSeo/webusage/internal/openusage"
	"github.com/ClaudeSeo/webusage/internal/store"
)

func TestCollectionSafetyAndCompletionContract(t *testing.T) {
	// Given: an enabled provider with a stored failure containing credentials.
	server, cleanup := setupTestServer(t)
	defer cleanup()
	providerID := mustCreateHTTPTestProvider(t, server, "claude", `{"auth_method":"oauth_file","cred_source":"/private/token","api_key":"private-secret"}`)
	mustEnableHTTPTestProvider(t, server, "claude")
	rawError := "request failed with access_token=private-secret at /private/token"
	if err := server.store.UpdateProviderStatus(providerID, &rawError); err != nil {
		t.Fatalf("UpdateProviderStatus() error = %v", err)
	}
	limit := 100.0
	mustCreateHTTPTestSnapshot(t, server, &store.UsageSnapshot{
		ProviderID:  providerID,
		Metric:      "session",
		Used:        42,
		Limit:       &limit,
		CollectedAt: time.Now().UTC(),
	})

	// When: current and forecast are requested after the failed collection.
	for _, path := range []string{"/api/current", "/api/forecast?provider_id=claude"} {
		recorder := httptest.NewRecorder()
		server.mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s status = %d; body = %s", path, recorder.Code, recorder.Body.String())
		}
		if !strings.Contains(recorder.Body.String(), `"collection_error":"collection failed"`) {
			t.Fatalf("%s omitted positive sanitized collection_error: %s", path, recorder.Body.String())
		}
		if strings.Contains(recorder.Body.String(), rawError) || strings.Contains(recorder.Body.String(), "private-secret") || strings.Contains(recorder.Body.String(), "/private/token") {
			t.Fatalf("%s leaked stored collection failure: %s", path, recorder.Body.String())
		}
	}

	// Then: the legacy error field remains available with the same sanitized value.
	currentRecorder := httptest.NewRecorder()
	server.mux.ServeHTTP(currentRecorder, httptest.NewRequest(http.MethodGet, "/api/current", nil))
	var current map[string]map[string]interface{}
	if err := json.Unmarshal(currentRecorder.Body.Bytes(), &current); err != nil {
		t.Fatalf("decode current = %v", err)
	}
	if got := current["claude"]["error"]; got != "collection failed" {
		t.Fatalf("current error = %#v, want sanitized value", got)
	}

	// Given: a collector that completes successfully without an OpenUsage client.
	server.SetCollector(collector.NewCollector(server.store, nil, time.Minute, slog.New(slog.NewTextHandler(io.Discard, nil))))
	postRecorder := httptest.NewRecorder()
	server.mux.ServeHTTP(postRecorder, httptest.NewRequest(http.MethodPost, "/api/collect", nil))
	if postRecorder.Code != http.StatusOK {
		t.Fatalf("POST /api/collect status = %d; body = %s", postRecorder.Code, postRecorder.Body.String())
	}
	var accepted map[string]interface{}
	if err := json.Unmarshal(postRecorder.Body.Bytes(), &accepted); err != nil {
		t.Fatalf("decode collect acceptance = %v", err)
	}
	if accepted["status"] != "collecting" {
		t.Fatalf("accepted status = %#v, want collecting", accepted["status"])
	}
	collectionID, ok := accepted["collection_id"].(string)
	if !ok || collectionID == "" {
		t.Fatalf("accepted collection_id = %#v, want non-empty id", accepted["collection_id"])
	}
	if accepted["status_url"] != "/api/collect/status?collection_id="+collectionID {
		t.Fatalf("accepted status_url = %#v", accepted["status_url"])
	}

	// When: the status resource is polled until the asynchronous run reaches a terminal state.
	status := waitForCollectionStatus(t, server, collectionID)
	// Then: completed is explicit and terminal, so the dashboard can refresh safely.
	if status["status"] != "completed" || status["terminal"] != true || status["done"] != true {
		t.Fatalf("completed status = %#v", status)
	}

	// Given: a collector whose OpenUsage request fails.
	failingServer, failingCleanup := setupTestServer(t)
	defer failingCleanup()
	openUsageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer openUsageServer.Close()
	failingServer.SetCollector(collector.NewCollector(failingServer.store, openusage.NewClient(openUsageServer.URL), time.Minute, slog.New(slog.NewTextHandler(io.Discard, nil))))

	// When: the failed collection is requested and its status is polled.
	failingPost := httptest.NewRecorder()
	failingServer.mux.ServeHTTP(failingPost, httptest.NewRequest(http.MethodPost, "/api/collect", nil))
	if failingPost.Code != http.StatusOK {
		t.Fatalf("failed POST /api/collect status = %d; body = %s", failingPost.Code, failingPost.Body.String())
	}
	var failedAccepted map[string]interface{}
	if err := json.Unmarshal(failingPost.Body.Bytes(), &failedAccepted); err != nil {
		t.Fatalf("decode failed acceptance = %v", err)
	}
	failedID, ok := failedAccepted["collection_id"].(string)
	if !ok || failedID == "" {
		t.Fatalf("failed collection_id = %#v", failedAccepted["collection_id"])
	}
	failedStatus := waitForCollectionStatus(t, failingServer, failedID)
	// Then: failed is also terminal and exposes only the sanitized collection error.
	if failedStatus["status"] != "failed" || failedStatus["terminal"] != true || failedStatus["done"] != true {
		t.Fatalf("failed status = %#v", failedStatus)
	}
	if failedStatus["collection_error"] != "collection failed" || failedStatus["error"] != "collection failed" {
		t.Fatalf("failed status error = %#v", failedStatus)
	}
	if strings.Contains(failingPost.Body.String(), "BadGateway") || strings.Contains(failingPost.Body.String(), "private-secret") {
		t.Fatalf("failed acceptance leaked an internal error: %s", failingPost.Body.String())
	}
}

func waitForCollectionStatus(t *testing.T, server *Server, collectionID string) map[string]interface{} {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		recorder := httptest.NewRecorder()
		path := "/api/collect/status?collection_id=" + collectionID
		server.mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d; body = %s", path, recorder.Code, recorder.Body.String())
		}
		var status map[string]interface{}
		if err := json.Unmarshal(recorder.Body.Bytes(), &status); err != nil {
			t.Fatalf("decode collection status = %v", err)
		}
		if terminal, _ := status["terminal"].(bool); terminal {
			return status
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("collection %q did not reach a terminal status", collectionID)
	return nil
}
