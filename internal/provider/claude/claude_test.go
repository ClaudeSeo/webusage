package claude

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ClaudeSeo/webusage/internal/oauth"
	"github.com/ClaudeSeo/webusage/internal/provider"
)

// mockRoundTripper는 네트워크 없이 HTTP 응답을 흉내내는 mock RoundTripper
type mockRoundTripper struct {
	// handler는 요청을 받아 응답 바이트와 상태코드를 반환
	handler func(req *http.Request) (int, string)
}

func (m *mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	statusCode, body := m.handler(req)
	return &http.Response{
		StatusCode: statusCode,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}, nil
}

func mockClient(handler func(req *http.Request) (int, string)) *http.Client {
	return &http.Client{Transport: &mockRoundTripper{handler: handler}}
}

// mockCredentialStore는 테스트용 인메모리 CredentialStore
type mockCredentialStore struct {
	tokens map[string]*oauth.Token
}

func newMockCredentialStore() *mockCredentialStore {
	return &mockCredentialStore{tokens: make(map[string]*oauth.Token)}
}

func (m *mockCredentialStore) Get(_ context.Context, providerName string) (*oauth.Token, error) {
	return m.tokens[providerName], nil
}

func (m *mockCredentialStore) Save(_ context.Context, providerName string, token *oauth.Token) error {
	m.tokens[providerName] = token
	return nil
}

func (m *mockCredentialStore) Delete(_ context.Context, providerName string) error {
	delete(m.tokens, providerName)
	return nil
}

func TestClaudeProvider_Name(t *testing.T) {
	p := New()
	if p.Name() != "claude" {
		t.Errorf("Name() = %q, want %q", p.Name(), "claude")
	}
	if p.DisplayName() != "Claude" {
		t.Errorf("DisplayName() = %q, want %q", p.DisplayName(), "Claude")
	}
}

func TestClaudeProvider_FetchUsage_Success(t *testing.T) {
	usageResp := usageResponse{
		FiveHour:       &usageWindow{Utilization: 0.45, ResetsAt: "2026-04-01T15:00:00Z"},
		SevenDay:       &usageWindow{Utilization: 0.72, ResetsAt: "2026-04-07T00:00:00Z"},
		SevenDaySonnet: &usageWindow{Utilization: 0.30, ResetsAt: "2026-04-07T00:00:00Z"},
		ExtraUsage:     &extraUsage{IsEnabled: true, UsedCredits: 5.50, MonthlyLimit: 100.0},
	}
	body, _ := json.Marshal(usageResp)

	client := mockClient(func(req *http.Request) (int, string) {
		if req.URL.Path == "/api/oauth/usage" {
			if req.Header.Get("anthropic-beta") != "oauth-2025-04-20" {
				return http.StatusBadRequest, `{"error":"missing beta header"}`
			}
			return http.StatusOK, string(body)
		}
		return http.StatusNotFound, `{"error":"not found"}`
	})

	store := newMockCredentialStore()
	exp := time.Now().Add(time.Hour)
	_ = store.Save(context.Background(), "claude", &oauth.Token{
		AccessToken: "test-access-token",
		ExpiresAt:   &exp,
	})

	p := New(
		WithBaseURL("http://mock"),
		WithHTTPClient(client),
		WithCredentialStore(store),
		WithSkipSystemCreds(),
	)

	points, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatalf("FetchUsage() error = %v", err)
	}
	// session(5h) + weekly(7d) + weekly_sonnet + extra_credits = 4개
	if len(points) != 4 {
		t.Fatalf("FetchUsage() returned %d points, want 4", len(points))
	}
	// session utilization 45%
	if points[0].Metric != "session" || points[0].Used != 45.0 {
		t.Errorf("session point = %v/%v, want session/45.0", points[0].Metric, points[0].Used)
	}
}

func TestClaudeProvider_FetchUsage_GracefulDegradation(t *testing.T) {
	// usage 엔드포인트가 500을 반환해도 빈 결과 반환
	client := mockClient(func(req *http.Request) (int, string) {
		return http.StatusInternalServerError, `{"error":"internal error"}`
	})

	store := newMockCredentialStore()
	exp := time.Now().Add(time.Hour)
	_ = store.Save(context.Background(), "claude", &oauth.Token{
		AccessToken: "test-access-token",
		ExpiresAt:   &exp,
	})

	p := New(
		WithBaseURL("http://mock"),
		WithHTTPClient(client),
		WithCredentialStore(store),
		WithSkipSystemCreds(),
	)

	points, err := p.FetchUsage(context.Background())
	// graceful degradation: error는 nil, 빈 결과 반환
	if err != nil {
		t.Errorf("FetchUsage() should not return error on degradation, got: %v", err)
	}
	if len(points) != 0 {
		t.Errorf("FetchUsage() returned %d points, want 0", len(points))
	}
}

func TestClaudeProvider_FetchSubscription(t *testing.T) {
	// FetchSubscription은 자격증명에서 추출한 정보를 반환 (API 호출 없음)
	store := newMockCredentialStore()
	exp := time.Now().Add(time.Hour)
	_ = store.Save(context.Background(), "claude", &oauth.Token{
		AccessToken: "test-access-token",
		ExpiresAt:   &exp,
	})

	cp := &ClaudeProvider{
		subscriptionType: "team",
		rateLimitTier:    "default_claude_max_5x",
		credStore:        store,
	}
	var p provider.Provider = cp

	info, err := p.FetchSubscription(context.Background())
	if err != nil {
		t.Fatalf("FetchSubscription() error = %v", err)
	}
	if info.SubscriptionType != "team" {
		t.Errorf("SubscriptionType = %q, want %q", info.SubscriptionType, "team")
	}
	if info.RateLimitTier != "default_claude_max_5x" {
		t.Errorf("RateLimitTier = %q, want %q", info.RateLimitTier, "default_claude_max_5x")
	}
}

func TestClaudeProvider_TokenRefresh(t *testing.T) {
	refreshCalled := false

	client := mockClient(func(req *http.Request) (int, string) {
		switch req.URL.Path {
		case "/v1/oauth/token":
			// refresh_token grant 검증
			body, _ := io.ReadAll(req.Body)
			if !strings.Contains(string(body), "grant_type=refresh_token") {
				return http.StatusBadRequest, `{"error":"wrong grant_type"}`
			}
			// client_id 포함 여부 검증
			if !strings.Contains(string(body), claudeOAuthClientID) {
				return http.StatusBadRequest, `{"error":"missing client_id"}`
			}
			refreshCalled = true
			resp := map[string]interface{}{
				"access_token":  "new-access-token",
				"refresh_token": "new-refresh-token",
				"token_type":    "Bearer",
				"expires_in":    3600,
			}
			b, _ := json.Marshal(resp)
			return http.StatusOK, string(b)
		case "/api/oauth/usage":
			// 새 토큰으로 요청이 오는지 확인
			authHeader := req.Header.Get("Authorization")
			if authHeader != "Bearer new-access-token" {
				return http.StatusUnauthorized, `{"error":"unauthorized"}`
			}
			b, _ := json.Marshal(usageResponse{})
			return http.StatusOK, string(b)
		default:
			return http.StatusNotFound, `{"error":"not found"}`
		}
	})

	store := newMockCredentialStore()
	// 14분 후 만료되는 토큰 (NeedsRefresh 조건: 15분 이내)
	exp := time.Now().Add(14 * time.Minute)
	_ = store.Save(context.Background(), "claude", &oauth.Token{
		AccessToken:  "old-access-token",
		RefreshToken: "old-refresh-token",
		ExpiresAt:    &exp,
	})

	p := New(
		WithBaseURL("http://mock"),
		WithTokenURL("http://mock/v1/oauth/token"),
		WithHTTPClient(client),
		WithCredentialStore(store),
		WithSkipSystemCreds(),
	)

	_, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatalf("FetchUsage() error = %v", err)
	}
	if !refreshCalled {
		t.Error("expected token refresh to be called, but it was not")
	}
}

func TestClaudeProvider_NeedsAuth(t *testing.T) {
	t.Run("valid token", func(t *testing.T) {
		store := newMockCredentialStore()
		exp := time.Now().Add(time.Hour)
		_ = store.Save(context.Background(), "claude", &oauth.Token{
			AccessToken: "valid-token",
			ExpiresAt:   &exp,
		})
		p := New(WithCredentialStore(store), WithSkipSystemCreds())
		if p.NeedsAuth() {
			t.Error("NeedsAuth() should be false when valid token exists")
		}
	})

	t.Run("expired token", func(t *testing.T) {
		store := newMockCredentialStore()
		exp := time.Now().Add(-time.Hour) // 이미 만료
		_ = store.Save(context.Background(), "claude", &oauth.Token{
			AccessToken: "expired-token",
			ExpiresAt:   &exp,
		})
		p := New(WithCredentialStore(store), WithSkipSystemCreds())
		if !p.NeedsAuth() {
			t.Error("NeedsAuth() should be true when token is expired")
		}
	})
}

func TestClaudeProvider_DiscoverCredentials(t *testing.T) {
	t.Run("credential store에 유효한 토큰 있음", func(t *testing.T) {
		store := newMockCredentialStore()
		exp := time.Now().Add(time.Hour)
		_ = store.Save(context.Background(), "claude", &oauth.Token{
			AccessToken: "valid-token",
			ExpiresAt:   &exp,
		})
		p := New(WithCredentialStore(store))
		found, err := p.DiscoverCredentials(context.Background())
		if err != nil {
			t.Fatalf("DiscoverCredentials() error = %v", err)
		}
		if !found {
			t.Error("DiscoverCredentials() should return true when token exists")
		}
	})

	t.Run("자격증명 없음", func(t *testing.T) {
		p := New(WithSkipSystemCreds()) // system creds 건너뛰기
		found, err := p.DiscoverCredentials(context.Background())
		if err != nil {
			t.Fatalf("DiscoverCredentials() error = %v", err)
		}
		// 실제 환경에서는 ~/.claude/.credentials.json이 존재할 수도 있으므로
		// 단순히 에러가 없는지만 확인
		_ = found
	})
}

func TestClaudeProvider_RefreshAuth(t *testing.T) {
	t.Run("토큰 없음", func(t *testing.T) {
		p := New(WithSkipSystemCreds())
		err := p.RefreshAuth(context.Background())
		if err == nil {
			t.Error("RefreshAuth() should return error when no credentials found")
		}
	})

	t.Run("유효한 토큰 (갱신 불필요)", func(t *testing.T) {
		store := newMockCredentialStore()
		exp := time.Now().Add(time.Hour)
		_ = store.Save(context.Background(), "claude", &oauth.Token{
			AccessToken: "valid-token",
			ExpiresAt:   &exp,
		})
		p := New(WithCredentialStore(store), WithSkipSystemCreds())
		if err := p.RefreshAuth(context.Background()); err != nil {
			t.Errorf("RefreshAuth() unexpected error = %v", err)
		}
	})
}
