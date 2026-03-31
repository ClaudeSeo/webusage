package gemini

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// setupTestServer는 테스트용 mock HTTP 서버와 GeminiProvider를 생성합니다
func setupTestServer(t *testing.T, handler http.Handler) (*httptest.Server, *GeminiProvider) {
	t.Helper()
	srv := httptest.NewServer(handler)
	p := New(
		WithBaseURL(srv.URL),
		WithTokenURL(srv.URL+"/token"),
		WithHTTPClient(srv.Client()),
		WithTokens("test-access-token", "test-refresh-token"),
	)
	return srv, p
}

func TestGeminiProvider_Name(t *testing.T) {
	p := New()
	if p.Name() != "gemini" {
		t.Errorf("Name() = %q, want %q", p.Name(), "gemini")
	}
}

func TestGeminiProvider_DisplayName(t *testing.T) {
	p := New()
	if p.DisplayName() != "Google Gemini" {
		t.Errorf("DisplayName() = %q, want %q", p.DisplayName(), "Google Gemini")
	}
}

func TestGeminiProvider_NeedsAuth(t *testing.T) {
	p := New()
	if !p.NeedsAuth() {
		t.Error("NeedsAuth() should be true when no token")
	}

	p2 := New(WithTokens("some-token", ""))
	if p2.NeedsAuth() {
		t.Error("NeedsAuth() should be false when access token is set")
	}
}

func TestGeminiProvider_FetchUsage_Success(t *testing.T) {
	mockModels := map[string]interface{}{
		"models": []interface{}{
			map[string]interface{}{"name": "models/gemini-pro"},
		},
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1beta/models" {
			// Authorization 헤더 검증
			if r.Header.Get("Authorization") != "Bearer test-access-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(mockModels)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})

	srv, p := setupTestServer(t, handler)
	defer srv.Close()

	points, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatalf("FetchUsage() error: %v", err)
	}

	if len(points) == 0 {
		t.Fatal("expected at least 1 usage point")
	}

	if points[0].Metric != "api_access" {
		t.Errorf("metric = %q, want %q", points[0].Metric, "api_access")
	}
}

func TestGeminiProvider_FetchUsage_TokenRefresh(t *testing.T) {
	callCount := 0
	mockModels := map[string]interface{}{"models": []interface{}{}}
	newToken := "new-access-token"

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1beta/models":
			callCount++
			if callCount == 1 {
				// 첫 번째 요청은 401 반환 (토큰 만료 시뮬레이션)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			// 두 번째 요청은 성공 (새 토큰 사용)
			if r.Header.Get("Authorization") != "Bearer "+newToken {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(mockModels)

		case "/token":
			// Google OAuth2 토큰 갱신 응답
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(googleTokenResponse{
				AccessToken: newToken,
				TokenType:   "Bearer",
				ExpiresIn:   3600,
			})
		}
	})

	srv, p := setupTestServer(t, handler)
	defer srv.Close()

	points, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatalf("FetchUsage() with token refresh error: %v", err)
	}

	if len(points) == 0 {
		t.Fatal("expected at least 1 usage point after token refresh")
	}

	// 토큰이 갱신되었는지 확인
	if p.accessToken != newToken {
		t.Errorf("accessToken after refresh = %q, want %q", p.accessToken, newToken)
	}
}

func TestGeminiProvider_RefreshAuth(t *testing.T) {
	newToken := "refreshed-token"

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(googleTokenResponse{
				AccessToken: newToken,
				TokenType:   "Bearer",
				ExpiresIn:   3600,
			})
		}
	})

	srv, p := setupTestServer(t, handler)
	defer srv.Close()

	if err := p.RefreshAuth(context.Background()); err != nil {
		t.Fatalf("RefreshAuth() error: %v", err)
	}

	if p.accessToken != newToken {
		t.Errorf("accessToken after refresh = %q, want %q", p.accessToken, newToken)
	}
}

func TestGeminiProvider_RefreshAuth_NoRefreshToken(t *testing.T) {
	p := New(WithTokens("access-token", ""))
	err := p.RefreshAuth(context.Background())
	if err == nil {
		t.Error("RefreshAuth() should fail when no refresh token")
	}
}

func TestGeminiProvider_DiscoverCredentials_FileFound(t *testing.T) {
	// 임시 credentials 파일 생성
	tmpDir := t.TempDir()
	credFile := filepath.Join(tmpDir, "oauth_creds.json")

	creds := geminiOAuthCreds{
		AccessToken:  "test-access-token",
		RefreshToken: "test-refresh-token",
		TokenType:    "Bearer",
	}
	data, _ := json.Marshal(creds)
	if err := os.WriteFile(credFile, data, 0600); err != nil {
		t.Fatalf("failed to create test cred file: %v", err)
	}

	p := New(WithCredPath(credFile))
	found, err := p.DiscoverCredentials(context.Background())
	if err != nil {
		t.Fatalf("DiscoverCredentials() error: %v", err)
	}
	if !found {
		t.Error("DiscoverCredentials() should find credentials from file")
	}
	if p.accessToken != "test-access-token" {
		t.Errorf("accessToken = %q, want %q", p.accessToken, "test-access-token")
	}
	if p.refreshToken != "test-refresh-token" {
		t.Errorf("refreshToken = %q, want %q", p.refreshToken, "test-refresh-token")
	}
}

func TestGeminiProvider_DiscoverCredentials_FileNotFound(t *testing.T) {
	p := New(WithCredPath("/nonexistent/path/oauth_creds.json"))
	found, err := p.DiscoverCredentials(context.Background())
	if err != nil {
		t.Fatalf("DiscoverCredentials() should not error for missing file: %v", err)
	}
	if found {
		t.Error("DiscoverCredentials() should return false for missing file")
	}
}
