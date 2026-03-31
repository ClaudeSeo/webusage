package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/ClaudeSeo/webusage/internal/credfinder"
	"github.com/ClaudeSeo/webusage/internal/oauth"
	"github.com/ClaudeSeo/webusage/internal/provider"
)

const (
	defaultBaseURL = "https://api.anthropic.com"
	// tokenURL은 Claude OAuth 토큰 갱신 엔드포인트
	defaultTokenURL = "https://platform.claude.com/v1/oauth/token"
	// claudeOAuthClientID는 Claude CLI OAuth client ID
	claudeOAuthClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	// credentialPath는 Claude CLI가 저장하는 OAuth 토큰 파일 경로
	credentialPath = "~/.claude/.credentials.json"
	// keychainService는 macOS Keychain에서 Claude 자격증명을 찾는 서비스 이름
	keychainService = "Claude Code-credentials"
)

// claudeCredentials는 ~/.claude/.credentials.json 파일 구조
type claudeCredentials struct {
	OAuth *claudeOAuthCredential `json:"oauth"`
}

// claudeOAuthCredential는 Claude CLI OAuth 토큰 정보
type claudeOAuthCredential struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresAt    *int64 `json:"expires_at,omitempty"` // Unix timestamp (밀리초)
}

// usageResponse는 Claude OAuth usage API 응답 구조
// GET /api/oauth/usage (anthropic-beta: oauth-2025-04-20)
type usageResponse struct {
	Object string       `json:"object"`
	Data   []usageEntry `json:"data"`
}

type usageEntry struct {
	Timestamp    int64  `json:"timestamp"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	Model        string `json:"model"`
}

// subscriptionResponse는 Claude subscription API 응답 구조
type subscriptionResponse struct {
	Object           string `json:"object"`
	SubscriptionType string `json:"subscription_type"`
	RateLimitTier    string `json:"rate_limit_tier"`
	PlanName         string `json:"plan_name,omitempty"`
}

// ClaudeProvider는 Claude/Anthropic OAuth 기반 usage provider
type ClaudeProvider struct {
	httpClient *http.Client
	credStore  oauth.CredentialStore
	baseURL    string
	tokenURL   string
	logger     *log.Logger
}

// Option은 ClaudeProvider 설정 함수
type Option func(*ClaudeProvider)

// WithBaseURL은 API 기본 URL을 설정합니다 (테스트용)
func WithBaseURL(url string) Option {
	return func(p *ClaudeProvider) {
		p.baseURL = url
	}
}

// WithTokenURL은 OAuth 토큰 URL을 설정합니다 (테스트용)
func WithTokenURL(url string) Option {
	return func(p *ClaudeProvider) {
		p.tokenURL = url
	}
}

// WithHTTPClient는 HTTP 클라이언트를 설정합니다 (테스트용)
func WithHTTPClient(client *http.Client) Option {
	return func(p *ClaudeProvider) {
		p.httpClient = client
	}
}

// WithCredentialStore는 자격증명 저장소를 설정합니다
func WithCredentialStore(store oauth.CredentialStore) Option {
	return func(p *ClaudeProvider) {
		p.credStore = store
	}
}

// New는 ClaudeProvider 인스턴스를 생성합니다
func New(opts ...Option) *ClaudeProvider {
	p := &ClaudeProvider{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    defaultBaseURL,
		tokenURL:   defaultTokenURL,
		logger:     log.New(os.Stderr, "[claude] ", log.LstdFlags),
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Name은 provider 식별자를 반환합니다
func (p *ClaudeProvider) Name() string { return "claude" }

// DisplayName은 UI 표시용 이름을 반환합니다
func (p *ClaudeProvider) DisplayName() string { return "Claude" }

// AuthMethod는 인증 방식을 반환합니다
func (p *ClaudeProvider) AuthMethod() provider.AuthMethod { return provider.AuthOAuthFile }

// NeedsAuth는 인증이 필요한 상태인지 반환합니다
func (p *ClaudeProvider) NeedsAuth() bool {
	token, err := p.loadToken(context.Background())
	if err != nil || token == nil {
		return true
	}
	return token.IsExpired()
}

// DiscoverCredentials는 로컬 환경에서 Claude OAuth 자격증명을 탐색합니다.
// 탐색 순서:
// 1. ~/.claude/.credentials.json (Claude CLI)
// 2. macOS Keychain "Claude Code-credentials"
// 3. credential store (DB)
func (p *ClaudeProvider) DiscoverCredentials(ctx context.Context) (bool, error) {
	token, err := p.loadToken(ctx)
	if err != nil {
		return false, fmt.Errorf("discovering claude credentials: %w", err)
	}
	return token != nil && token.AccessToken != "", nil
}

// RefreshAuth는 토큰 갱신을 수행합니다
func (p *ClaudeProvider) RefreshAuth(ctx context.Context) error {
	token, err := p.loadToken(ctx)
	if err != nil {
		return fmt.Errorf("loading token: %w", err)
	}
	if token == nil {
		return fmt.Errorf("no credentials found: set up Claude CLI (claude login)")
	}
	if token.IsExpired() && token.RefreshToken != "" {
		return p.refreshToken(ctx, token)
	}
	return nil
}

// FetchUsage는 Claude OAuth usage API에서 사용량을 조회합니다.
// 토큰 갱신이 필요하면 내부에서 처리합니다.
// 에러 발생 시 graceful degradation으로 빈 결과를 반환합니다.
func (p *ClaudeProvider) FetchUsage(ctx context.Context) ([]provider.UsagePoint, error) {
	token, err := p.getValidToken(ctx)
	if err != nil {
		// graceful degradation: 에러 로그 후 빈 결과 반환
		p.logger.Printf("failed to get valid token: %v", err)
		return []provider.UsagePoint{}, nil
	}

	// OAuth usage 엔드포인트: /api/oauth/usage
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/api/oauth/usage", nil)
	if err != nil {
		p.logger.Printf("failed to create usage request: %v", err)
		return []provider.UsagePoint{}, nil
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	// OAuth usage API는 별도 beta 헤더 필요
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		p.logger.Printf("usage request failed: %v", err)
		return []provider.UsagePoint{}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		p.logger.Printf("usage endpoint returned %d", resp.StatusCode)
		return []provider.UsagePoint{}, nil
	}

	var usageResp usageResponse
	if err := json.NewDecoder(resp.Body).Decode(&usageResp); err != nil {
		p.logger.Printf("failed to decode usage response: %v", err)
		return []provider.UsagePoint{}, nil
	}

	rawBytes, _ := json.Marshal(usageResp)
	now := time.Now()

	var totalInput, totalOutput int64
	for _, entry := range usageResp.Data {
		totalInput += entry.InputTokens
		totalOutput += entry.OutputTokens
	}
	total := float64(totalInput + totalOutput)

	return []provider.UsagePoint{
		{
			Metric:      "tokens",
			Used:        total,
			CollectedAt: now,
			RawJSON:     string(rawBytes),
		},
	}, nil
}

// FetchSubscription은 Claude 구독 정보를 조회합니다
func (p *ClaudeProvider) FetchSubscription(ctx context.Context) (*provider.SubscriptionInfo, error) {
	token, err := p.getValidToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting valid token: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/v1/account/subscription", nil)
	if err != nil {
		return nil, fmt.Errorf("creating subscription request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("subscription request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("subscription endpoint returned %d", resp.StatusCode)
	}

	var subResp subscriptionResponse
	if err := json.NewDecoder(resp.Body).Decode(&subResp); err != nil {
		return nil, fmt.Errorf("decoding subscription response: %w", err)
	}

	rawBytes, _ := json.Marshal(subResp)

	return &provider.SubscriptionInfo{
		ProviderName:     p.Name(),
		PlanName:         subResp.PlanName,
		SubscriptionType: subResp.SubscriptionType,
		RateLimitTier:    subResp.RateLimitTier,
		RawJSON:          string(rawBytes),
	}, nil
}

// loadToken은 자격증명을 우선순위에 따라 로드합니다:
// 1. ~/.claude/.credentials.json (Claude CLI OAuth 토큰)
// 2. macOS Keychain "Claude Code-credentials"
// 3. credential store (DB/파일)
func (p *ClaudeProvider) loadToken(ctx context.Context) (*oauth.Token, error) {
	// 1순위: ~/.claude/.credentials.json (Claude CLI)
	var creds claudeCredentials
	if err := credfinder.ReadJSONCredential(credentialPath, &creds); err == nil {
		if token := credToToken(creds.OAuth); token != nil {
			return token, nil
		}
	}

	// 2순위: macOS Keychain "Claude Code-credentials"
	if raw, err := credfinder.KeychainItem(keychainService, ""); err == nil && raw != "" {
		var keychainCreds claudeCredentials
		if jsonErr := json.Unmarshal([]byte(raw), &keychainCreds); jsonErr == nil {
			if token := credToToken(keychainCreds.OAuth); token != nil {
				return token, nil
			}
		}
	}

	// 3순위: credential store (DB)
	if p.credStore != nil {
		if token, err := p.credStore.Get(ctx, p.Name()); err == nil && token != nil {
			return token, nil
		}
	}

	return nil, nil
}

// credToToken은 claudeOAuthCredential을 oauth.Token으로 변환합니다
func credToToken(cred *claudeOAuthCredential) *oauth.Token {
	if cred == nil || cred.AccessToken == "" {
		return nil
	}
	token := &oauth.Token{
		AccessToken:  cred.AccessToken,
		RefreshToken: cred.RefreshToken,
	}
	// ExpiresAt: Unix 밀리초 → time.Time 변환
	if cred.ExpiresAt != nil {
		exp := time.Unix(*cred.ExpiresAt/1000, 0)
		token.ExpiresAt = &exp
	}
	return token
}

// getValidToken은 유효한 토큰을 반환합니다.
// 15분 내 만료 예정이면 선제적으로 갱신합니다. (FetchUsage 내부에서 처리)
func (p *ClaudeProvider) getValidToken(ctx context.Context) (*oauth.Token, error) {
	token, err := p.loadToken(ctx)
	if err != nil {
		return nil, err
	}
	if token == nil {
		return nil, fmt.Errorf("no credentials available")
	}

	// 선제적 갱신: 만료 15분 전
	if token.NeedsRefresh() && token.RefreshToken != "" {
		if refreshErr := p.refreshToken(ctx, token); refreshErr != nil {
			// 갱신 실패해도 현재 토큰이 아직 유효하면 계속 사용
			p.logger.Printf("token refresh failed (using current): %v", refreshErr)
			if token.IsExpired() {
				return nil, fmt.Errorf("token expired and refresh failed: %w", refreshErr)
			}
		}
	}

	return token, nil
}

// refreshToken은 refresh_token으로 새 access_token을 발급합니다.
// Claude OAuth client_id: 9d1c250a-e61b-44d9-88ed-5944d1962f5e
func (p *ClaudeProvider) refreshToken(ctx context.Context, token *oauth.Token) error {
	cfg := oauth.OAuth2Config{
		TokenURL: p.tokenURL,
		ClientID: claudeOAuthClientID,
	}
	newToken, err := oauth.RefreshTokenFlow(ctx, cfg, token.RefreshToken, p.httpClient)
	if err != nil {
		return fmt.Errorf("refreshing token: %w", err)
	}

	// 갱신된 토큰을 store에 저장
	if p.credStore != nil {
		if saveErr := p.credStore.Save(ctx, p.Name(), newToken); saveErr != nil {
			p.logger.Printf("failed to save refreshed token: %v", saveErr)
		}
	}

	// 현재 토큰 값 업데이트
	token.AccessToken = newToken.AccessToken
	if newToken.RefreshToken != "" {
		token.RefreshToken = newToken.RefreshToken
	}
	token.ExpiresAt = newToken.ExpiresAt

	return nil
}
