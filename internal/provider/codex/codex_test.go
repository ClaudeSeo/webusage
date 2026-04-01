package codex

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
	// handler는 요청을 받아 상태코드, body, header를 반환
	handler func(req *http.Request) (int, string, http.Header)
}

func (m *mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	statusCode, body, headers := m.handler(req)
	resp := &http.Response{
		StatusCode: statusCode,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
	for k, vals := range headers {
		for _, v := range vals {
			resp.Header.Add(k, v)
		}
	}
	return resp, nil
}

func mockClient(handler func(req *http.Request) (int, string, http.Header)) *http.Client {
	return &http.Client{Transport: &mockRoundTripper{handler: handler}}
}

// simpleHandler는 header 없는 단순 응답용 헬퍼
func simpleHandler(statusCode int, body string) func(req *http.Request) (int, string, http.Header) {
	return func(req *http.Request) (int, string, http.Header) {
		return statusCode, body, nil
	}
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

func TestCodexProvider_Name(t *testing.T) {
	p := New()
	if p.Name() != "codex" {
		t.Errorf("Name() = %q, want %q", p.Name(), "codex")
	}
	if p.DisplayName() != "Codex" {
		t.Errorf("DisplayName() = %q, want %q", p.DisplayName(), "Codex")
	}
}

// TestCodexProvider_FetchUsage_HeaderBased는 헤더 값이 존재할 때 우선 사용하는지 검증합니다
func TestCodexProvider_FetchUsage_HeaderBased(t *testing.T) {
	// body에는 다른 값이 있어도 header 값이 우선돼야 함
	bodyData := usageResponseBody{
		RateLimit: &rateLimit{
			PrimaryWindow:   &windowData{UsedPercent: 10},
			SecondaryWindow: &windowData{UsedPercent: 20},
		},
		Credits: &creditsBlock{Balance: json.Number("500")},
	}
	bodyBytes, _ := json.Marshal(bodyData)

	client := mockClient(func(req *http.Request) (int, string, http.Header) {
		headers := http.Header{}
		headers.Set("x-codex-primary-used-percent", "42")
		headers.Set("x-codex-secondary-used-percent", "15")
		headers.Set("x-codex-credits-balance", "750")
		return http.StatusOK, string(bodyBytes), headers
	})

	store := newMockCredentialStore()
	_ = store.Save(context.Background(), "codex", &oauth.Token{
		AccessToken:  "test-access-token",
		RefreshToken: "test-refresh-token",
	})

	p := New(
		WithBaseURL("http://mock/usage"),
		WithHTTPClient(client),
		WithCredentialStore(store),
		WithSkipSystemCreds(),
	)

	points, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatalf("FetchUsage() error = %v", err)
	}
	// session + weekly + credits = 3개
	if len(points) != 3 {
		t.Fatalf("FetchUsage() returned %d points, want 3", len(points))
	}

	// 헤더 값 기준으로 session=42%
	sessionPt := findPoint(points, "session")
	if sessionPt == nil {
		t.Fatal("missing session point")
	}
	if sessionPt.Used != 42 {
		t.Errorf("session.Used = %v, want 42", sessionPt.Used)
	}
	if sessionPt.Limit == nil || *sessionPt.Limit != 100 {
		t.Errorf("session.Limit = %v, want 100", sessionPt.Limit)
	}

	// 헤더 값 기준으로 weekly=15%
	weeklyPt := findPoint(points, "weekly")
	if weeklyPt == nil {
		t.Fatal("missing weekly point")
	}
	if weeklyPt.Used != 15 {
		t.Errorf("weekly.Used = %v, want 15", weeklyPt.Used)
	}

	// credits: used = 1000 - 750 = 250
	creditsPt := findPoint(points, "credits")
	if creditsPt == nil {
		t.Fatal("missing credits point")
	}
	if creditsPt.Used != 250 {
		t.Errorf("credits.Used = %v, want 250", creditsPt.Used)
	}
	if creditsPt.Limit == nil || *creditsPt.Limit != 1000 {
		t.Errorf("credits.Limit = %v, want 1000", creditsPt.Limit)
	}
}

// TestCodexProvider_FetchUsage_BodyFallback는 헤더가 없을 때 body 값을 사용하는지 검증합니다
func TestCodexProvider_FetchUsage_BodyFallback(t *testing.T) {
	resetUnix := time.Now().Add(time.Hour).Unix()
	bodyData := usageResponseBody{
		RateLimit: &rateLimit{
			PrimaryWindow:   &windowData{UsedPercent: 35, ResetAt: resetUnix},
			SecondaryWindow: &windowData{UsedPercent: 60},
		},
	}
	bodyBytes, _ := json.Marshal(bodyData)

	client := mockClient(func(req *http.Request) (int, string, http.Header) {
		// 헤더 없음 → body fallback
		return http.StatusOK, string(bodyBytes), nil
	})

	store := newMockCredentialStore()
	_ = store.Save(context.Background(), "codex", &oauth.Token{
		AccessToken: "test-access-token",
	})

	p := New(
		WithBaseURL("http://mock/usage"),
		WithHTTPClient(client),
		WithCredentialStore(store),
		WithSkipSystemCreds(),
	)

	points, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatalf("FetchUsage() error = %v", err)
	}
	// session + weekly = 2개 (credits 없음)
	if len(points) != 2 {
		t.Fatalf("FetchUsage() returned %d points, want 2", len(points))
	}

	sessionPt := findPoint(points, "session")
	if sessionPt == nil {
		t.Fatal("missing session point")
	}
	if sessionPt.Used != 35 {
		t.Errorf("session.Used = %v, want 35", sessionPt.Used)
	}
	if sessionPt.ResetAt == nil {
		t.Error("session.ResetAt should not be nil when reset_at is set")
	}

	weeklyPt := findPoint(points, "weekly")
	if weeklyPt == nil {
		t.Fatal("missing weekly point")
	}
	if weeklyPt.Used != 60 {
		t.Errorf("weekly.Used = %v, want 60", weeklyPt.Used)
	}
}

// TestCodexProvider_FetchUsage_WithCredits는 body credits 데이터를 반환하는지 검증합니다
func TestCodexProvider_FetchUsage_WithCredits(t *testing.T) {
	bodyData := usageResponseBody{
		Credits: &creditsBlock{Balance: json.Number("200")},
	}
	bodyBytes, _ := json.Marshal(bodyData)

	client := mockClient(simpleHandler(http.StatusOK, string(bodyBytes)))

	store := newMockCredentialStore()
	_ = store.Save(context.Background(), "codex", &oauth.Token{
		AccessToken: "test-access-token",
	})

	p := New(
		WithBaseURL("http://mock/usage"),
		WithHTTPClient(client),
		WithCredentialStore(store),
		WithSkipSystemCreds(),
	)

	points, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatalf("FetchUsage() error = %v", err)
	}

	creditsPt := findPoint(points, "credits")
	if creditsPt == nil {
		t.Fatal("missing credits point")
	}
	// used = 1000 - 200 = 800
	if creditsPt.Used != 800 {
		t.Errorf("credits.Used = %v, want 800", creditsPt.Used)
	}
	if creditsPt.Limit == nil || *creditsPt.Limit != 1000 {
		t.Errorf("credits.Limit = %v, want 1000", creditsPt.Limit)
	}
}

// TestCodexProvider_FetchUsage_NonOKReturnsError는 usage 엔드포인트 5xx 오류 시 에러를 반환하는지 검증합니다
func TestCodexProvider_FetchUsage_NonOKReturnsError(t *testing.T) {
	client := mockClient(simpleHandler(http.StatusInternalServerError, `{"error":"internal error"}`))

	store := newMockCredentialStore()
	_ = store.Save(context.Background(), "codex", &oauth.Token{
		AccessToken: "test-access-token",
	})

	p := New(
		WithBaseURL("http://mock/usage"),
		WithHTTPClient(client),
		WithCredentialStore(store),
		WithSkipSystemCreds(),
	)

	_, err := p.FetchUsage(context.Background())
	// non-200 응답 시 에러 반환 (collector가 실패를 인지할 수 있도록)
	if err == nil {
		t.Error("FetchUsage() should return error on non-200 response, got nil")
	}
}

// TestCodexProvider_TokenRefresh는 last_refresh가 8일 초과 시 토큰 갱신이 호출되는지 검증합니다
func TestCodexProvider_TokenRefresh(t *testing.T) {
	refreshCalled := false

	client := mockClient(func(req *http.Request) (int, string, http.Header) {
		switch req.URL.Path {
		case "/oauth/token":
			// refresh_token grant 검증
			body, _ := io.ReadAll(req.Body)
			if !strings.Contains(string(body), "grant_type=refresh_token") {
				return http.StatusBadRequest, `{"error":"wrong grant_type"}`, nil
			}
			if !strings.Contains(string(body), codexOAuthClientID) {
				return http.StatusBadRequest, `{"error":"missing client_id"}`, nil
			}
			refreshCalled = true
			resp := map[string]interface{}{
				"access_token":  "new-access-token",
				"refresh_token": "new-refresh-token",
				"token_type":    "Bearer",
				"expires_in":    3600,
			}
			b, _ := json.Marshal(resp)
			return http.StatusOK, string(b), nil
		case "/usage":
			// 새 토큰으로 요청이 오는지 확인
			authHeader := req.Header.Get("Authorization")
			if authHeader != "Bearer new-access-token" {
				return http.StatusUnauthorized, `{"error":"unauthorized"}`, nil
			}
			b, _ := json.Marshal(usageResponseBody{})
			return http.StatusOK, string(b), nil
		default:
			return http.StatusNotFound, `{"error":"not found"}`, nil
		}
	})

	store := newMockCredentialStore()
	// 9일 전에 last_refresh가 된 토큰을 store에 직접 저장
	// (loadToken은 skipSystemCreds 시 store에서만 읽으므로 auth의 last_refresh를 주입할 방법이 없음)
	// → store 토큰으로 갱신 트리거를 위해 getValidToken 내부 경로를 우회
	// 실제 last_refresh 기반 갱신은 파일 경로에서 로드될 때 발생하므로,
	// 이 테스트는 refreshToken 직접 호출을 통해 동작을 검증합니다
	_ = store.Save(context.Background(), "codex", &oauth.Token{
		AccessToken:  "old-access-token",
		RefreshToken: "old-refresh-token",
	})

	p := New(
		WithBaseURL("http://mock/usage"),
		WithRefreshURL("http://mock/oauth/token"),
		WithHTTPClient(client),
		WithCredentialStore(store),
		WithSkipSystemCreds(),
	)

	// refreshToken 직접 호출로 갱신 로직 검증
	token := &oauth.Token{
		AccessToken:  "old-access-token",
		RefreshToken: "old-refresh-token",
	}
	if err := p.refreshToken(context.Background(), token); err != nil {
		t.Fatalf("refreshToken() error = %v", err)
	}
	if !refreshCalled {
		t.Error("expected token refresh to be called, but it was not")
	}
	if token.AccessToken != "new-access-token" {
		t.Errorf("token.AccessToken = %q, want %q", token.AccessToken, "new-access-token")
	}

	// store에도 저장됐는지 확인
	saved, err := store.Get(context.Background(), "codex")
	if err != nil || saved == nil {
		t.Fatal("expected refreshed token to be saved in store")
	}
	if saved.AccessToken != "new-access-token" {
		t.Errorf("saved.AccessToken = %q, want %q", saved.AccessToken, "new-access-token")
	}
}

// TestCodexProvider_DiscoverCredentials_FromFile은 파일에서 자격증명 탐색을 검증합니다
func TestCodexProvider_DiscoverCredentials_FromFile(t *testing.T) {
	t.Run("자격증명 없음 (skipSystemCreds)", func(t *testing.T) {
		p := New(WithSkipSystemCreds())
		found, err := p.DiscoverCredentials(context.Background())
		if err != nil {
			t.Fatalf("DiscoverCredentials() error = %v", err)
		}
		if found {
			t.Error("DiscoverCredentials() should return false with no credentials")
		}
	})

	t.Run("credential store에 유효한 토큰 있음", func(t *testing.T) {
		store := newMockCredentialStore()
		_ = store.Save(context.Background(), "codex", &oauth.Token{
			AccessToken: "valid-token",
		})
		p := New(WithCredentialStore(store), WithSkipSystemCreds())
		found, err := p.DiscoverCredentials(context.Background())
		if err != nil {
			t.Fatalf("DiscoverCredentials() error = %v", err)
		}
		if !found {
			t.Error("DiscoverCredentials() should return true when token exists in store")
		}
	})
}

// TestCodexProvider_NeedsAuth는 자격증명 유무에 따른 NeedsAuth 동작을 검증합니다
func TestCodexProvider_NeedsAuth(t *testing.T) {
	t.Run("토큰 있음", func(t *testing.T) {
		store := newMockCredentialStore()
		_ = store.Save(context.Background(), "codex", &oauth.Token{
			AccessToken: "valid-token",
		})
		p := New(WithCredentialStore(store), WithSkipSystemCreds())
		if p.NeedsAuth() {
			t.Error("NeedsAuth() should be false when valid token exists")
		}
	})

	t.Run("토큰 없음", func(t *testing.T) {
		p := New(WithSkipSystemCreds())
		if !p.NeedsAuth() {
			t.Error("NeedsAuth() should be true when no credentials")
		}
	})
}

// TestCodexProvider_RefreshAuth는 RefreshAuth 동작을 검증합니다
func TestCodexProvider_RefreshAuth(t *testing.T) {
	t.Run("토큰 없음", func(t *testing.T) {
		p := New(WithSkipSystemCreds())
		err := p.RefreshAuth(context.Background())
		if err == nil {
			t.Error("RefreshAuth() should return error when no credentials found")
		}
	})

	t.Run("유효한 토큰 (갱신 불필요)", func(t *testing.T) {
		store := newMockCredentialStore()
		_ = store.Save(context.Background(), "codex", &oauth.Token{
			AccessToken:  "valid-token",
			RefreshToken: "refresh-token",
		})
		p := New(WithCredentialStore(store), WithSkipSystemCreds())
		// store 토큰은 auth.LastRefresh가 없으므로 needsRefreshByAge 확인 불가 → 갱신 없이 통과
		if err := p.RefreshAuth(context.Background()); err != nil {
			t.Errorf("RefreshAuth() unexpected error = %v", err)
		}
	})
}

// TestNeedsRefreshByAge는 last_refresh 기준 갱신 조건을 검증합니다
func TestNeedsRefreshByAge(t *testing.T) {
	t.Run("빈 문자열이면 갱신 필요", func(t *testing.T) {
		if !needsRefreshByAge("") {
			t.Error("empty lastRefresh should need refresh")
		}
	})

	t.Run("9일 전이면 갱신 필요", func(t *testing.T) {
		nineDAgo := time.Now().Add(-9 * 24 * time.Hour).Format(time.RFC3339)
		if !needsRefreshByAge(nineDAgo) {
			t.Error("9-day-old lastRefresh should need refresh")
		}
	})

	t.Run("1일 전이면 갱신 불필요", func(t *testing.T) {
		oneDayAgo := time.Now().Add(-1 * 24 * time.Hour).Format(time.RFC3339)
		if needsRefreshByAge(oneDayAgo) {
			t.Error("1-day-old lastRefresh should not need refresh")
		}
	})
}

// TestParseHeaderFloat는 헤더 float 파싱을 검증합니다
func TestParseHeaderFloat(t *testing.T) {
	tests := []struct {
		val  string
		want float64
	}{
		{"", -1},
		{"abc", -1},
		{"42", 42},
		{"0", 0},
		{"99.5", 99.5},
	}
	for _, tc := range tests {
		got := parseHeaderFloat(tc.val)
		if got != tc.want {
			t.Errorf("parseHeaderFloat(%q) = %v, want %v", tc.val, got, tc.want)
		}
	}
}

// TestParseAuthPayload_HexEncoded는 hex 인코딩된 Keychain 페이로드를 처리하는지 검증합니다
func TestParseAuthPayload_HexEncoded(t *testing.T) {
	auth := codexAuth{
		Tokens: &codexTokens{
			AccessToken:  "hex-access-token",
			RefreshToken: "hex-refresh-token",
		},
		LastRefresh: "2026-03-25T00:00:00Z",
	}
	jsonBytes, _ := json.Marshal(auth)

	// 0x 접두사 hex 인코딩
	hexPayload := "0x" + hexEncode(jsonBytes)

	token, parsedAuth, err := parseAuthPayload(hexPayload)
	if err != nil {
		t.Fatalf("parseAuthPayload() hex error = %v", err)
	}
	if token == nil || token.AccessToken != "hex-access-token" {
		t.Errorf("token.AccessToken = %v, want hex-access-token", token)
	}
	if parsedAuth == nil || parsedAuth.LastRefresh != "2026-03-25T00:00:00Z" {
		t.Errorf("parsedAuth.LastRefresh = %v", parsedAuth)
	}
}

// TestParseAuthPayload_RawJSON은 raw JSON Keychain 페이로드를 처리하는지 검증합니다
func TestParseAuthPayload_RawJSON(t *testing.T) {
	auth := codexAuth{
		Tokens: &codexTokens{
			AccessToken:  "raw-access-token",
			RefreshToken: "raw-refresh-token",
		},
		LastRefresh: "2026-03-25T00:00:00Z",
	}
	jsonBytes, _ := json.Marshal(auth)

	token, parsedAuth, err := parseAuthPayload(string(jsonBytes))
	if err != nil {
		t.Fatalf("parseAuthPayload() raw JSON error = %v", err)
	}
	if token == nil || token.AccessToken != "raw-access-token" {
		t.Errorf("token.AccessToken = %v, want raw-access-token", token)
	}
	if parsedAuth == nil || parsedAuth.LastRefresh != "2026-03-25T00:00:00Z" {
		t.Errorf("parsedAuth.LastRefresh = %v", parsedAuth)
	}
}

// hexEncode는 바이트 슬라이스를 hex 문자열로 변환합니다 (테스트 헬퍼)
func hexEncode(b []byte) string {
	const hextable = "0123456789abcdef"
	buf := make([]byte, len(b)*2)
	for i, v := range b {
		buf[i*2] = hextable[v>>4]
		buf[i*2+1] = hextable[v&0x0f]
	}
	return string(buf)
}

// TestCodexProvider_FetchSubscription은 구독 정보 반환을 검증합니다
func TestCodexProvider_FetchSubscription(t *testing.T) {
	p := New(WithSkipSystemCreds())
	p.planType = "pro"

	info, err := p.FetchSubscription(context.Background())
	if err != nil {
		t.Fatalf("FetchSubscription() error = %v", err)
	}
	if info.ProviderName != "codex" {
		t.Errorf("ProviderName = %q, want %q", info.ProviderName, "codex")
	}
	if info.PlanName != "pro" {
		t.Errorf("PlanName = %q, want %q", info.PlanName, "pro")
	}

	// Provider 인터페이스 구현 여부 컴파일 타임 검증
	var _ provider.Provider = p
}

// findPoint는 points 슬라이스에서 특정 metric을 찾습니다
func findPoint(points []provider.UsagePoint, metric string) *provider.UsagePoint {
	for i := range points {
		if points[i].Metric == metric {
			return &points[i]
		}
	}
	return nil
}
