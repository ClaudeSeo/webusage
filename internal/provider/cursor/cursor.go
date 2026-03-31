package cursor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/ClaudeSeo/webusage/internal/credfinder"
	"github.com/ClaudeSeo/webusage/internal/provider"
)

const (
	defaultAPIURL  = "https://api2.cursor.sh"
	defaultAuthURL = "https://api2.cursor.sh/auth/full_stripe_profile"
)

// cursorStripeProfile은 Cursor billing API 응답 구조
type cursorStripeProfile struct {
	MembershipType string `json:"membershipType"`
	// UsageBasedPricing은 사용량 기반 청구 여부
	UsageBasedPricing *usageBasedPricing `json:"usageBasedPricing,omitempty"`
}

type usageBasedPricing struct {
	AutoTopUpThresholdCents int64 `json:"autoTopUpThresholdCents,omitempty"`
}

// cursorUsageResponse는 Cursor usage API 응답 구조
type cursorUsageResponse struct {
	GptPlus *gptUsage `json:"gpt-4,omitempty"`
	GptSlow *gptUsage `json:"gpt-3.5-turbo,omitempty"`
	// numRequestsTotal: 총 요청 수
	NumRequestsTotal int64 `json:"numRequestsTotal,omitempty"`
	// numRequestsFast: 빠른 요청 수
	NumRequestsFast int64 `json:"numRequestsFast,omitempty"`
	// numRequestsSlow: 느린 요청 수
	NumRequestsSlow int64 `json:"numRequestsSlow,omitempty"`
	// startOfMonth: 청구 주기 시작일
	StartOfMonth *string `json:"startOfMonth,omitempty"`
}

type gptUsage struct {
	NumRequests      int64 `json:"numRequests"`
	NumRequestsLimit int64 `json:"numRequestsLimit"`
	NumTokens        int64 `json:"numTokens"`
}

// oauthTokenResponse는 Cursor OAuth 토큰 갱신 응답 구조
type oauthTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
}

// CursorProvider는 Cursor OAuth 기반 usage provider
// SQLite DB 또는 Keychain에서 OAuth 토큰을 탐색합니다
type CursorProvider struct {
	dbPath     string
	accessToken string
	userID     string
	baseURL    string
	httpClient *http.Client
	logger     *log.Logger
}

// Option은 CursorProvider 설정 함수
type Option func(*CursorProvider)

// WithDBPath는 Cursor SQLite DB 경로를 설정합니다
func WithDBPath(path string) Option {
	return func(p *CursorProvider) {
		p.dbPath = path
	}
}

// WithBaseURL은 API 기본 URL을 설정합니다 (테스트용)
func WithBaseURL(url string) Option {
	return func(p *CursorProvider) {
		p.baseURL = url
	}
}

// WithHTTPClient는 HTTP 클라이언트를 설정합니다 (테스트용)
func WithHTTPClient(client *http.Client) Option {
	return func(p *CursorProvider) {
		p.httpClient = client
	}
}

// WithAccessToken은 액세스 토큰을 직접 설정합니다 (테스트용)
func WithAccessToken(token string) Option {
	return func(p *CursorProvider) {
		p.accessToken = token
	}
}

// WithUserID는 사용자 ID를 직접 설정합니다 (테스트용)
func WithUserID(userID string) Option {
	return func(p *CursorProvider) {
		p.userID = userID
	}
}

// New는 CursorProvider 인스턴스를 생성합니다
func New(opts ...Option) *CursorProvider {
	p := &CursorProvider{
		baseURL:    defaultAPIURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		logger:     log.New(os.Stderr, "[cursor] ", log.LstdFlags),
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Name은 provider 식별자를 반환합니다
func (p *CursorProvider) Name() string { return "cursor" }

// DisplayName은 UI 표시용 이름을 반환합니다
func (p *CursorProvider) DisplayName() string { return "Cursor" }

// AuthMethod는 로컬 DB 기반 인증 방식을 반환합니다
func (p *CursorProvider) AuthMethod() provider.AuthMethod { return provider.AuthLocalDB }

// NeedsAuth는 액세스 토큰이 없을 때 true를 반환합니다
func (p *CursorProvider) NeedsAuth() bool { return p.accessToken == "" }

// DiscoverCredentials는 로컬 Cursor SQLite DB 또는 Keychain에서 OAuth 토큰을 탐색합니다.
// 탐색 순서:
// 1. SQLite DB (dbPath 또는 OS별 기본 경로)
// 2. macOS Keychain fallback
func (p *CursorProvider) DiscoverCredentials(_ context.Context) (bool, error) {
	// 1순위: SQLite DB에서 토큰 탐색
	dbPath := p.dbPath
	if dbPath == "" {
		dbPath = defaultCursorDBPath()
	}

	if dbPath != "" {
		token, err := credfinder.ReadSQLiteValue(dbPath, "ItemTable", "cursorAuth/accessToken")
		if err == nil && token != "" {
			// JWT에서 userId 추출
			userID, err := credfinder.ExtractUserID(token)
			if err != nil {
				p.logger.Printf("failed to extract userId from JWT: %v", err)
			} else {
				p.userID = userID
			}
			p.accessToken = token
			return true, nil
		}
		if !errors.Is(err, credfinder.ErrNotFound) && err != nil {
			p.logger.Printf("failed to read cursor sqlite db: %v", err)
		}
	}

	// 2순위: macOS Keychain fallback
	token, err := credfinder.KeychainItem("cursor-access-token", "")
	if err != nil {
		if errors.Is(err, credfinder.ErrNotFound) || errors.Is(err, credfinder.ErrNotSupported) {
			return false, nil
		}
		return false, fmt.Errorf("reading cursor keychain: %w", err)
	}

	if token == "" {
		return false, nil
	}

	userID, err := credfinder.ExtractUserID(token)
	if err != nil {
		p.logger.Printf("failed to extract userId from keychain JWT: %v", err)
	} else {
		p.userID = userID
	}
	p.accessToken = token
	return true, nil
}

// RefreshAuth는 Cursor OAuth 토큰을 갱신합니다
func (p *CursorProvider) RefreshAuth(ctx context.Context) error {
	if p.accessToken == "" {
		return fmt.Errorf("cursor: no access token to refresh")
	}

	// Cursor OAuth 토큰 갱신 요청
	body := map[string]string{
		"refresh_token": p.accessToken,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("cursor: marshaling refresh request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.baseURL+"/oauth/token", bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("cursor: creating refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("cursor: refresh request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("cursor: token refresh returned %d", resp.StatusCode)
	}

	var tokenResp oauthTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return fmt.Errorf("cursor: decoding refresh response: %w", err)
	}

	p.accessToken = tokenResp.AccessToken
	return nil
}

// FetchUsage는 Cursor usage API에서 사용량을 조회합니다.
// 토큰이 없으면 내부에서 DiscoverCredentials를 시도합니다.
func (p *CursorProvider) FetchUsage(ctx context.Context) ([]provider.UsagePoint, error) {
	// 토큰 없으면 탐색 시도
	if p.accessToken == "" {
		found, err := p.DiscoverCredentials(ctx)
		if err != nil {
			return nil, fmt.Errorf("cursor: credential discovery failed: %w", err)
		}
		if !found {
			return nil, fmt.Errorf("cursor: credentials not found")
		}
	}

	// Cursor usage API 호출
	// userId를 쿠키로 전달하여 사용량 조회
	url := p.baseURL + "/auth/usage"
	if p.userID != "" {
		url = fmt.Sprintf("%s?user=%s", url, neturl.QueryEscape(p.userID))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating cursor usage request: %w", err)
	}
	// Cursor API는 쿠키 기반 인증 사용
	if p.userID != "" {
		req.Header.Set("Cookie", fmt.Sprintf("WorkosCursorSessionToken=%s::%s",
			p.userID, p.accessToken))
	} else {
		req.Header.Set("Cookie", fmt.Sprintf("WorkosCursorSessionToken=%s", p.accessToken))
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cursor usage request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("cursor: token unauthorized (status 401)")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cursor usage endpoint returned %d", resp.StatusCode)
	}

	var usageResp cursorUsageResponse
	if err := json.NewDecoder(resp.Body).Decode(&usageResp); err != nil {
		return nil, fmt.Errorf("decoding cursor usage response: %w", err)
	}

	rawBytes, _ := json.Marshal(usageResp)
	now := time.Now()

	var points []provider.UsagePoint
	var billingStart *time.Time
	if usageResp.StartOfMonth != nil {
		if t, err := time.Parse("2006-01-02", *usageResp.StartOfMonth); err == nil {
			billingStart = &t
		}
	}

	// 총 요청 수
	if usageResp.NumRequestsTotal > 0 {
		points = append(points, provider.UsagePoint{
			Metric:      "requests_total",
			Used:        float64(usageResp.NumRequestsTotal),
			ResetAt:     billingStart,
			CollectedAt: now,
			RawJSON:     string(rawBytes),
		})
	}

	// 빠른 요청 수 (GPT-4 등 프리미엄)
	if usageResp.NumRequestsFast > 0 || usageResp.GptPlus != nil {
		used := float64(usageResp.NumRequestsFast)
		var limit *float64
		if usageResp.GptPlus != nil {
			used = float64(usageResp.GptPlus.NumRequests)
			if usageResp.GptPlus.NumRequestsLimit > 0 {
				l := float64(usageResp.GptPlus.NumRequestsLimit)
				limit = &l
			}
		}
		points = append(points, provider.UsagePoint{
			Metric:      "requests_fast",
			Used:        used,
			Limit:       limit,
			ResetAt:     billingStart,
			CollectedAt: now,
			RawJSON:     string(rawBytes),
		})
	}

	return points, nil
}

// FetchSubscription은 Cursor 구독 정보를 조회합니다
func (p *CursorProvider) FetchSubscription(ctx context.Context) (*provider.SubscriptionInfo, error) {
	if p.accessToken == "" {
		return nil, fmt.Errorf("cursor: not authenticated")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		p.baseURL+"/auth/full_stripe_profile", nil)
	if err != nil {
		return nil, fmt.Errorf("creating cursor subscription request: %w", err)
	}
	if p.userID != "" {
		req.Header.Set("Cookie", fmt.Sprintf("WorkosCursorSessionToken=%s::%s",
			p.userID, p.accessToken))
	} else {
		req.Header.Set("Cookie", fmt.Sprintf("WorkosCursorSessionToken=%s", p.accessToken))
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cursor subscription request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cursor subscription endpoint returned %d", resp.StatusCode)
	}

	var profile cursorStripeProfile
	if err := json.NewDecoder(resp.Body).Decode(&profile); err != nil {
		return nil, fmt.Errorf("decoding cursor subscription response: %w", err)
	}

	rawBytes, _ := json.Marshal(profile)

	return &provider.SubscriptionInfo{
		ProviderName: p.Name(),
		PlanName:     profile.MembershipType,
		RawJSON:      string(rawBytes),
	}, nil
}

// defaultCursorDBPath는 OS별 기본 Cursor SQLite DB 경로를 반환합니다
func defaultCursorDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	switch runtime.GOOS {
	case "darwin":
		// macOS: ~/Library/Application Support/Cursor/User/globalStorage/storage.vscdb
		return filepath.Join(home, "Library", "Application Support",
			"Cursor", "User", "globalStorage", "storage.vscdb")
	case "linux":
		// Linux: ~/.config/Cursor/User/globalStorage/storage.vscdb
		return filepath.Join(home, ".config", "Cursor", "User",
			"globalStorage", "storage.vscdb")
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			appData = filepath.Join(home, "AppData", "Roaming")
		}
		return filepath.Join(appData, "Cursor", "User",
			"globalStorage", "storage.vscdb")
	}
	return ""
}
