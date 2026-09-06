package http

import (
	"encoding/json"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ClaudeSeo/webusage/internal/store"
)

func mustCreateHTTPTestProvidersWithSnapshots(t *testing.T, server *Server, names ...string) {
	t.Helper()
	for _, name := range names {
		providerID := mustCreateHTTPTestProvider(t, server, name, `{"auth_method":"api_key"}`)
		mustCreateHTTPTestSnapshot(t, server, &store.UsageSnapshot{
			ProviderID:  providerID,
			Metric:      "credits",
			Used:        10,
			CollectedAt: time.Now().UTC(),
		})
	}
}

func putProviderOrder(t *testing.T, server *Server, payload string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	server.mux.ServeHTTP(recorder, httptest.NewRequest(nethttp.MethodPut, "/api/provider-order", strings.NewReader(payload)))
	return recorder
}

func providerOrderIDs(t *testing.T, body []byte) []string {
	t.Helper()
	var payload struct {
		ProviderIDs []string `json:"provider_ids"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decoding provider order response %q: %v", string(body), err)
	}
	return payload.ProviderIDs
}

func assertStringSequence(t *testing.T, got, want []string, label string) {
	t.Helper()
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
}

// attributeSequence collects, in body order, the quoted value that follows each
// occurrence of prefix. It follows the body-index pattern of
// dashboard_design_test.go and turns SSR markup order into a comparable list.
func attributeSequence(t *testing.T, body, prefix string) []string {
	t.Helper()
	var values []string
	for offset := 0; ; {
		start := strings.Index(body[offset:], prefix)
		if start < 0 {
			break
		}
		start += offset + len(prefix)
		end := strings.Index(body[start:], `"`)
		if end < 0 {
			t.Fatalf("unterminated attribute value after %q", prefix)
		}
		values = append(values, body[start:start+end])
		offset = start + end
	}
	return values
}

func TestProviderOrderShouldPersistAndReflectInProvidersAPI(t *testing.T) {
	// Given: providers registered in alphabetical default order.
	server, cleanup := setupTestServer(t)
	defer cleanup()
	mustCreateHTTPTestProvidersWithSnapshots(t, server, "kiro", "ollama", "claude")

	// When: saving a new provider order.
	recorder := putProviderOrder(t, server, `{"provider_ids":["ollama","claude","kiro"]}`)
	if recorder.Code != nethttp.StatusOK {
		t.Fatalf("provider order status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	assertStringSequence(t, providerOrderIDs(t, recorder.Body.Bytes()), []string{"ollama", "claude", "kiro"}, "saved provider_ids")

	// Then: GET /api/providers reflects the saved order.
	recorder = httptest.NewRecorder()
	server.mux.ServeHTTP(recorder, httptest.NewRequest(nethttp.MethodGet, "/api/providers", nil))
	if recorder.Code != nethttp.StatusOK {
		t.Fatalf("providers API status = %d, want 200", recorder.Code)
	}
	var payload struct {
		Providers []struct {
			ProviderID string `json:"provider_id"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decoding providers API response: %v", err)
	}
	var ids []string
	for _, provider := range payload.Providers {
		ids = append(ids, provider.ProviderID)
	}
	assertStringSequence(t, ids, []string{"ollama", "claude", "kiro"}, "providers API order")
}

func TestProviderOrderShouldRejectInvalidPayloads(t *testing.T) {
	// Given: a server with registered providers.
	server, cleanup := setupTestServer(t)
	defer cleanup()
	mustCreateHTTPTestProvidersWithSnapshots(t, server, "kiro", "ollama")

	// When: submitting payloads that duplicate, invent, or omit providers.
	for _, test := range []struct {
		name    string
		payload string
	}{
		{"duplicate provider", `{"provider_ids":["kiro","kiro","ollama"]}`},
		{"unknown provider", `{"provider_ids":["kiro","ollama","ghost"]}`},
		{"empty provider list", `{"provider_ids":[]}`},
	} {
		recorder := putProviderOrder(t, server, test.payload)
		if recorder.Code != nethttp.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400: %s", test.name, recorder.Code, recorder.Body.String())
		}
	}

	// Then: reads are rejected instead of silently returning the saved order.
	recorder := httptest.NewRecorder()
	server.mux.ServeHTTP(recorder, httptest.NewRequest(nethttp.MethodGet, "/api/provider-order", nil))
	if recorder.Code != nethttp.StatusMethodNotAllowed {
		t.Errorf("GET provider order status = %d, want 405", recorder.Code)
	}
}

func TestProviderOrderShouldRenderDashboardInSavedOrder(t *testing.T) {
	// Given: providers that the SSR dashboard renders alphabetically.
	server, cleanup := setupTestServer(t)
	defer cleanup()
	mustCreateHTTPTestProvidersWithSnapshots(t, server, "kiro", "ollama", "claude")

	// When: a saved provider order exists and the dashboard is rendered.
	recorder := putProviderOrder(t, server, `{"provider_ids":["ollama","claude","kiro"]}`)
	if recorder.Code != nethttp.StatusOK {
		t.Fatalf("provider order status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	server.mux.ServeHTTP(recorder, httptest.NewRequest(nethttp.MethodGet, "/", nil))
	if recorder.Code != nethttp.StatusOK {
		t.Fatalf("dashboard status = %d, want 200", recorder.Code)
	}
	body := recorder.Body.String()

	// Then: first-paint cards and metric table rows follow the saved order.
	assertStringSequence(t,
		attributeSequence(t, body, `<article class="provider-card interactive" data-slot="card" data-provider-id="`),
		[]string{"ollama", "claude", "kiro"},
		"dashboard card order")
	assertStringSequence(t,
		attributeSequence(t, body, `<tr data-provider="`),
		[]string{"ollama", "claude", "kiro"},
		"dashboard table row order")
}
