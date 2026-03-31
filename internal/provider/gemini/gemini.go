package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/ClaudeSeo/webusage/internal/credfinder"
	"github.com/ClaudeSeo/webusage/internal/provider"
)

const (
	defaultCredPath  = "~/.gemini/oauth_creds.json"
	googleTokenURL   = "https://oauth2.googleapis.com/token"
	// geminiClientID는 gemini CLI의 OAuth client ID
	geminiClientID   = "681254893541-oc6bsagqeq3em4f35qlc0mq4fh4bndf2.apps.googleusercontent.com"
)

// geminiOAuthCreds는 ~/.gemini/oauth_creds.json 파일 구조
type geminiOAuthCreds struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresAt    int64  `json:"expires_at,omitempty"` // Unix timestamp
}

// googleTokenResponse는 Google OAuth2 토큰 갱신 응답
type googleTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
}

// GeminiProvider는 Google Gemini OAuth 파일 기반 usage provider
// ~/.gemini/oauth_creds.json 파일에서 OAuth 토큰을 탐색합니다
type GeminiProvider struct {
	credPath    string
	accessToken string
	refreshToken string
	baseURL     string
	tokenURL    string
	httpClient  *http.Client
	logger      *log.Logger
}

// Option은 GeminiProvider 설정 함수
type Option func(*GeminiProvider)

// WithCredPath는 OAuth 자격증명 파일 경로를 설정합니다 (테스트용)
func WithCredPath(path string) Option {
	return func(p *GeminiProvider) {
		p.credPath = path
	}
}

// WithBaseURL은 API 기본 URL을 설정합니다 (테스트용)
func WithBaseURL(url string) Option {
	return func(p *GeminiProvider) {
		p.baseURL = url
	}
}

// WithTokenURL은 OAuth 토큰 URL을 설정합니다 (테스트용)
func WithTokenURL(url string) Option {
	return func(p *GeminiProvider) {
		p.tokenURL = url
	}
}

// WithHTTPClient는 HTTP 클라이언트를 설정합니다 (테스트용)
func WithHTTPClient(client *http.Client) Option {
	return func(p *GeminiProvider) {
		p.httpClient = client
	}
}

// WithTokens는 액세스 토큰과 리프레시 토큰을 직접 설정합니다 (테스트용)
func WithTokens(accessToken, refreshToken string) Option {
	return func(p *GeminiProvider) {
		p.accessToken = accessToken
		p.refreshToken = refreshToken
	}
}

// New는 GeminiProvider 인스턴스를 생성합니다
func New(opts ...Option) *GeminiProvider {
	p := &GeminiProvider{
		credPath:   defaultCredPath,
		baseURL:    "https://generativelanguage.googleapis.com",
		tokenURL:   googleTokenURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		logger:     log.New(os.Stderr, "[gemini] ", log.LstdFlags),
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Name은 provider 식별자를 반환합니다
func (p *GeminiProvider) Name() string { return "gemini" }

// DisplayName은 UI 표시용 이름을 반환합니다
func (p *GeminiProvider) DisplayName() string { return "Google Gemini" }

// AuthMethod는 OAuth 파일 기반 인증 방식을 반환합니다
func (p *GeminiProvider) AuthMethod() provider.AuthMethod { return provider.AuthOAuthFile }

// NeedsAuth는 액세스 토큰이 없을 때 true를 반환합니다
func (p *GeminiProvider) NeedsAuth() bool { return p.accessToken == "" }

// DiscoverCredentials는 로컬 Gemini OAuth 파일을 탐색합니다.
// ~/.gemini/oauth_creds.json 파일에서 OAuth 토큰을 읽습니다.
func (p *GeminiProvider) DiscoverCredentials(_ context.Context) (bool, error) {
	var creds geminiOAuthCreds
	err := credfinder.ReadJSONCredential(p.credPath, &creds)
	if err != nil {
		if errors.Is(err, credfinder.ErrNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("reading gemini credentials: %w", err)
	}

	if creds.AccessToken == "" && creds.RefreshToken == "" {
		return false, nil
	}

	p.accessToken = creds.AccessToken
	p.refreshToken = creds.RefreshToken
	return true, nil
}

// RefreshAuth는 Google OAuth2로 액세스 토큰을 갱신합니다
func (p *GeminiProvider) RefreshAuth(ctx context.Context) error {
	if p.refreshToken == "" {
		return fmt.Errorf("gemini: no refresh token available")
	}

	body := map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": p.refreshToken,
		"client_id":     geminiClientID,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("gemini: marshaling refresh request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.tokenURL,
		bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("gemini: creating refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("gemini: refresh request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("gemini: token refresh returned %d", resp.StatusCode)
	}

	var tokenResp googleTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return fmt.Errorf("gemini: decoding refresh response: %w", err)
	}

	p.accessToken = tokenResp.AccessToken
	return nil
}

// FetchUsage는 Gemini usage 정보를 조회합니다.
// 토큰이 없으면 내부에서 DiscoverCredentials를 시도합니다.
// 토큰 만료 시 RefreshAuth로 갱신합니다.
func (p *GeminiProvider) FetchUsage(ctx context.Context) ([]provider.UsagePoint, error) {
	// 토큰 없으면 탐색 시도
	if p.accessToken == "" {
		found, err := p.DiscoverCredentials(ctx)
		if err != nil {
			return nil, fmt.Errorf("gemini: credential discovery failed: %w", err)
		}
		if !found {
			return nil, fmt.Errorf("gemini: credentials not found at %s", p.credPath)
		}
	}

	// Google AI Studio quota API 호출
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		p.baseURL+"/v1beta/models", nil)
	if err != nil {
		return nil, fmt.Errorf("gemini: creating usage request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.accessToken)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gemini: usage request failed: %w", err)
	}
	defer resp.Body.Close()

	// 401이면 토큰 갱신 후 재시도
	if resp.StatusCode == http.StatusUnauthorized {
		if p.refreshToken == "" {
			return nil, fmt.Errorf("gemini: token expired, no refresh token available")
		}
		if err := p.RefreshAuth(ctx); err != nil {
			return nil, fmt.Errorf("gemini: token refresh failed: %w", err)
		}

		// 재시도
		req2, err := http.NewRequestWithContext(ctx, http.MethodGet,
			p.baseURL+"/v1beta/models", nil)
		if err != nil {
			return nil, fmt.Errorf("gemini: creating retry request: %w", err)
		}
		req2.Header.Set("Authorization", "Bearer "+p.accessToken)
		resp2, err := p.httpClient.Do(req2)
		if err != nil {
			return nil, fmt.Errorf("gemini: retry request failed: %w", err)
		}
		defer resp2.Body.Close()
		resp = resp2
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gemini: usage endpoint returned %d", resp.StatusCode)
	}

	// 응답을 raw JSON으로 저장
	var rawData interface{}
	if err := json.NewDecoder(resp.Body).Decode(&rawData); err != nil {
		return nil, fmt.Errorf("gemini: decoding usage response: %w", err)
	}

	rawBytes, _ := json.Marshal(rawData)
	now := time.Now()

	// Gemini는 현재 공개 usage API가 없으므로 subscription 정보 위주로 수집
	// 모델 목록 조회 성공 자체가 인증 유효성을 나타냅니다
	return []provider.UsagePoint{
		{
			Metric:      "api_access",
			Used:        1,
			CollectedAt: now,
			RawJSON:     string(rawBytes),
		},
	}, nil
}

// FetchSubscription은 Gemini 구독 정보를 조회합니다
func (p *GeminiProvider) FetchSubscription(ctx context.Context) (*provider.SubscriptionInfo, error) {
	if p.accessToken == "" {
		return nil, fmt.Errorf("gemini: not authenticated")
	}

	// Google AI Studio 사용자 정보 조회
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://aistudio.googleapis.com/v1/billingPlans", nil)
	if err != nil {
		return nil, fmt.Errorf("creating gemini subscription request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.accessToken)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gemini subscription request failed: %w", err)
	}
	defer resp.Body.Close()

	var rawData interface{}
	if err := json.NewDecoder(resp.Body).Decode(&rawData); err != nil {
		return nil, fmt.Errorf("decoding gemini subscription response: %w", err)
	}

	rawBytes, _ := json.Marshal(rawData)

	planName := "Google AI Studio"
	if resp.StatusCode == http.StatusOK {
		planName = "Google AI Studio (Active)"
	}

	return &provider.SubscriptionInfo{
		ProviderName: p.Name(),
		PlanName:     planName,
		RawJSON:      string(rawBytes),
	}, nil
}
