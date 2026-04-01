package cursor

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// setupTestServer는 테스트용 mock HTTP 서버와 CursorProvider를 생성합니다
func setupTestServer(t *testing.T, handler http.Handler) (*httptest.Server, *CursorProvider) {
	t.Helper()
	srv := httptest.NewServer(handler)
	p := New(
		WithBaseURL(srv.URL),
		WithHTTPClient(srv.Client()),
		WithAccessToken("test-cursor-token"),
		WithUserID("test-user-id"),
	)
	return srv, p
}

func TestCursorProvider_Name(t *testing.T) {
	p := New()
	if p.Name() != "cursor" {
		t.Errorf("Name() = %q, want %q", p.Name(), "cursor")
	}
}

func TestCursorProvider_DisplayName(t *testing.T) {
	p := New()
	if p.DisplayName() != "Cursor" {
		t.Errorf("DisplayName() = %q, want %q", p.DisplayName(), "Cursor")
	}
}

func TestCursorProvider_NeedsAuth(t *testing.T) {
	p := New()
	if !p.NeedsAuth() {
		t.Error("NeedsAuth() should be true when no token")
	}

	p2 := New(WithAccessToken("some-token"))
	if p2.NeedsAuth() {
		t.Error("NeedsAuth() should be false when token is set")
	}
}

func TestCursorProvider_FetchUsage_Success(t *testing.T) {
	startOfMonth := "2026-03-01"
	mockResp := cursorUsageResponse{
		NumRequestsTotal: 150,
		NumRequestsFast:  100,
		NumRequestsSlow:  50,
		StartOfMonth:     &startOfMonth,
		GptPlus: &gptUsage{
			NumRequests:      100,
			NumRequestsLimit: 500,
			NumTokens:        50000,
		},
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/usage" {
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

	if len(points) == 0 {
		t.Fatal("expected at least 1 usage point")
	}

	// requests_total 검증
	var totalFound bool
	for _, point := range points {
		if point.Metric == "requests_total" {
			totalFound = true
			if point.Used != 150 {
				t.Errorf("requests_total used = %f, want 150", point.Used)
			}
		}
	}
	if !totalFound {
		t.Error("missing requests_total metric")
	}
}

func TestCursorProvider_FetchUsage_Unauthorized(t *testing.T) {
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

func TestCursorProvider_FetchSubscription(t *testing.T) {
	mockProfile := cursorStripeProfile{
		MembershipType: "pro",
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/full_stripe_profile" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(mockProfile)
	})

	srv, p := setupTestServer(t, handler)
	defer srv.Close()

	info, err := p.FetchSubscription(context.Background())
	if err != nil {
		t.Fatalf("FetchSubscription() error: %v", err)
	}

	if info.ProviderName != "cursor" {
		t.Errorf("ProviderName = %q, want %q", info.ProviderName, "cursor")
	}
	if info.PlanName != "pro" {
		t.Errorf("PlanName = %q, want %q", info.PlanName, "pro")
	}
}

func TestCursorProvider_DiscoverCredentials_SQLite(t *testing.T) {
	// 임시 SQLite DB 생성
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "storage.vscdb")

	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s", dbPath))
	if err != nil {
		t.Fatalf("failed to create test sqlite db: %v", err)
	}

	// ItemTable 생성 및 테스트 토큰 삽입
	_, err = db.Exec(`CREATE TABLE ItemTable (key TEXT, value TEXT)`)
	if err != nil {
		t.Fatalf("failed to create table: %v", err)
	}

	// 테스트용 가짜 JWT 토큰 (서명 없이 base64 페이로드만)
	// 실제 JWT: header.payload.signature
	// payload: {"sub": "test-user-123"}
	testToken := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ0ZXN0LXVzZXItMTIzIn0.fake-signature"
	_, err = db.Exec(`INSERT INTO ItemTable (key, value) VALUES (?, ?)`,
		"cursorAuth/accessToken", testToken)
	if err != nil {
		t.Fatalf("failed to insert test token: %v", err)
	}
	db.Close()

	p := New(WithDBPath(dbPath))
	found, err := p.DiscoverCredentials(context.Background())
	if err != nil {
		t.Fatalf("DiscoverCredentials() error: %v", err)
	}
	if !found {
		t.Error("DiscoverCredentials() should find token in SQLite DB")
	}
	if p.accessToken != testToken {
		t.Errorf("accessToken = %q, want %q", p.accessToken, testToken)
	}
	if p.userID != "test-user-123" {
		t.Errorf("userID = %q, want %q", p.userID, "test-user-123")
	}
}

func TestCursorProvider_DiscoverCredentials_NotFound(t *testing.T) {
	// 존재하지 않는 DB 경로 + Keychain fallback 건너뛰기
	p := New(WithDBPath("/nonexistent/path/storage.vscdb"), WithSkipSystemCreds())
	found, err := p.DiscoverCredentials(context.Background())
	// 에러가 나거나 found=false여야 합니다
	if found {
		t.Error("DiscoverCredentials() should not find credentials for nonexistent path")
	}
	_ = err // 에러는 있어도 없어도 됩니다
}

func TestDefaultCursorDBPath(t *testing.T) {
	// 현재 OS에서 기본 경로가 비어있지 않은지 확인 (경로 계산 로직 검증)
	path := defaultCursorDBPath()
	// 빈 문자열이 아니어야 합니다 (home dir를 얻을 수 있는 환경에서)
	if _, err := os.UserHomeDir(); err == nil && path == "" {
		t.Error("defaultCursorDBPath() should return non-empty path when home dir is available")
	}
}
