package copilot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// setupTestServer는 테스트용 mock HTTP 서버를 생성합니다
func setupTestServer(t *testing.T, handler http.Handler) (*httptest.Server, *CopilotProvider) {
	t.Helper()
	srv := httptest.NewServer(handler)
	p := New(
		WithBaseURL(srv.URL),
		WithHTTPClient(srv.Client()),
		WithToken("test-github-token"),
	)
	return srv, p
}

func TestCopilotProvider_Name(t *testing.T) {
	p := New()
	if p.Name() != "copilot" {
		t.Errorf("Name() = %q, want %q", p.Name(), "copilot")
	}
}

func TestCopilotProvider_DisplayName(t *testing.T) {
	p := New()
	if p.DisplayName() != "GitHub Copilot" {
		t.Errorf("DisplayName() = %q, want %q", p.DisplayName(), "GitHub Copilot")
	}
}

func TestCopilotProvider_NeedsAuth_WithToken(t *testing.T) {
	p := New(WithToken("some-token"))
	if p.NeedsAuth() {
		t.Error("NeedsAuth() should be false when token is set")
	}
}

func TestCopilotProvider_NeedsAuth_NoToken(t *testing.T) {
	p := New()
	if !p.NeedsAuth() {
		t.Error("NeedsAuth() should be true when no token")
	}
}

func TestCopilotProvider_FetchUsage_Success(t *testing.T) {
	resetDate := "2026-04-01T00:00:00Z"
	mockResp := copilotUsageResponse{
		PremiumInteractions: &quotaInfo{Quota: 300, Used: 50},
		Chat:                &quotaInfo{Quota: 10000, Used: 1234},
		NextResetDate:       &resetDate,
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Authorization 헤더 검증
		if r.Header.Get("Authorization") != "Bearer test-github-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/copilot_internal/user" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(mockResp)
	})

	srv, p := setupTestServer(t, handler)
	defer srv.Close()

	points, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatalf("FetchUsage() error: %v", err)
	}

	if len(points) != 2 {
		t.Fatalf("expected 2 usage points, got %d", len(points))
	}

	// premium_interactions 검증
	var premiumPoint, chatPoint *usagePointRef
	for i := range points {
		switch points[i].Metric {
		case "premium_interactions":
			pp := &usagePointRef{used: points[i].Used, limit: points[i].Limit}
			premiumPoint = pp
		case "chat":
			cp := &usagePointRef{used: points[i].Used, limit: points[i].Limit}
			chatPoint = cp
		}
	}

	if premiumPoint == nil {
		t.Fatal("missing premium_interactions metric")
	}
	if premiumPoint.used != 50 {
		t.Errorf("premium_interactions used = %f, want 50", premiumPoint.used)
	}
	if premiumPoint.limit == nil || *premiumPoint.limit != 300 {
		t.Errorf("premium_interactions limit = %v, want 300", premiumPoint.limit)
	}

	if chatPoint == nil {
		t.Fatal("missing chat metric")
	}
	if chatPoint.used != 1234 {
		t.Errorf("chat used = %f, want 1234", chatPoint.used)
	}
}

// usagePointRef는 테스트에서 UsagePoint 값을 쉽게 참조하기 위한 헬퍼 구조체
type usagePointRef struct {
	used  float64
	limit *float64
}

func TestCopilotProvider_FetchUsage_Unauthorized(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})

	srv, p := setupTestServer(t, handler)
	defer srv.Close()

	_, err := p.FetchUsage(context.Background())
	if err == nil {
		t.Error("FetchUsage() should return error for 401 response")
	}
}

func TestCopilotProvider_FetchUsage_EmptyQuotas(t *testing.T) {
	mockResp := copilotUsageResponse{}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(mockResp)
	})

	srv, p := setupTestServer(t, handler)
	defer srv.Close()

	points, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatalf("FetchUsage() error: %v", err)
	}

	// 할당량이 없으면 빈 결과 반환
	if len(points) != 0 {
		t.Errorf("expected 0 usage points for empty quotas, got %d", len(points))
	}
}

func TestCopilotProvider_FetchSubscription(t *testing.T) {
	resetDate := "2026-04-01T00:00:00Z"
	mockResp := copilotUsageResponse{
		PremiumInteractions: &quotaInfo{Quota: 300, Used: 50},
		NextResetDate:       &resetDate,
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(mockResp)
	})

	srv, p := setupTestServer(t, handler)
	defer srv.Close()

	info, err := p.FetchSubscription(context.Background())
	if err != nil {
		t.Fatalf("FetchSubscription() error: %v", err)
	}

	if info.ProviderName != "copilot" {
		t.Errorf("ProviderName = %q, want %q", info.ProviderName, "copilot")
	}
	if info.PlanName == "" {
		t.Error("PlanName should not be empty")
	}
}
