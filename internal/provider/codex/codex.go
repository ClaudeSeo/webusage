package codex

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ClaudeSeo/webusage/internal/credfinder"
	"github.com/ClaudeSeo/webusage/internal/oauth"
	"github.com/ClaudeSeo/webusage/internal/provider"
)

const (
	// defaultUsageURL은 Codex CLI usage 엔드포인트
	defaultUsageURL = "https://chatgpt.com/backend-api/wham/usage"
	// defaultRefreshURL은 OpenAI OAuth 토큰 갱신 엔드포인트
	defaultRefreshURL = "https://auth.openai.com/oauth/token"
	// codexOAuthClientID는 Codex CLI OAuth client ID
	codexOAuthClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
	// keychainService는 macOS Keychain에서 Codex 자격증명을 찾는 서비스 이름
	keychainService = "Codex Auth"
	// credPathConfig는 Codex 기본 config 경로
	credPathConfig = "~/.config/codex/auth.json"
	// credPathLegacy는 Codex 레거시 config 경로
	credPathLegacy = "~/.codex/auth.json"
	// refreshAgeDays는 last_refresh 기준 토큰 갱신 주기 (8일)
	refreshAgeDays = 8 * 24 * time.Hour
	// creditsMax는 Codex 크레딧 최대값 (limit 계산 기준)
	creditsMax = 1000.0
)

// codexAuth는 auth.json 파일 구조
type codexAuth struct {
	Tokens      *codexTokens `json:"tokens"`
	LastRefresh string       `json:"last_refresh"` // ISO8601
}

// codexTokens는 auth.json 내 tokens 블록
type codexTokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token,omitempty"`
	AccountID    string `json:"account_id,omitempty"`
}

// usageResponseBody는 Codex usage API body 응답 구조
type usageResponseBody struct {
	PlanType  string        `json:"plan_type,omitempty"`
	RateLimit *rateLimit    `json:"rate_limit,omitempty"`
	Credits   *creditsBlock `json:"credits,omitempty"`
}

type rateLimit struct {
	PrimaryWindow   *windowData `json:"primary_window,omitempty"`
	SecondaryWindow *windowData `json:"secondary_window,omitempty"`
}

type windowData struct {
	UsedPercent        float64 `json:"used_percent"`
	ResetAt            int64   `json:"reset_at,omitempty"`             // Unix 타임스탬프
	ResetAfterSeconds  int64   `json:"reset_after_seconds,omitempty"`  // reset_at 대체
	LimitWindowSeconds int64   `json:"limit_window_seconds,omitempty"` // 창 크기 (초)
}

type creditsBlock struct {
	Balance float64 `json:"balance"`
}

// CodexProvider는 OpenAI Codex CLI OAuth 기반 usage provider
type CodexProvider struct {
	httpClient      *http.Client
	credStore       oauth.CredentialStore
	usageURL        string
	refreshURL      string
	logger          *log.Logger
	planType        string // FetchUsage 시 body에서 추출
	skipSystemCreds bool   // true면 파일/Keychain 건너뛰기 (테스트용)
}

// Option은 CodexProvider 설정 함수
type Option func(*CodexProvider)

// WithBaseURL은 usage API URL을 설정합니다 (테스트용)
func WithBaseURL(url string) Option {
	return func(p *CodexProvider) {
		p.usageURL = url
	}
}

// WithRefreshURL은 OAuth 토큰 갱신 URL을 설정합니다 (테스트용)
func WithRefreshURL(url string) Option {
	return func(p *CodexProvider) {
		p.refreshURL = url
	}
}

// WithHTTPClient는 HTTP 클라이언트를 설정합니다 (테스트용)
func WithHTTPClient(client *http.Client) Option {
	return func(p *CodexProvider) {
		p.httpClient = client
	}
}

// WithCredentialStore는 자격증명 저장소를 설정합니다
func WithCredentialStore(store oauth.CredentialStore) Option {
	return func(p *CodexProvider) {
		p.credStore = store
	}
}

// WithSkipSystemCreds는 파일/Keychain 탐색을 건너뛰고 credStore만 사용합니다 (테스트용)
func WithSkipSystemCreds() Option {
	return func(p *CodexProvider) {
		p.skipSystemCreds = true
	}
}

// New는 CodexProvider 인스턴스를 생성합니다
func New(opts ...Option) *CodexProvider {
	p := &CodexProvider{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		usageURL:   defaultUsageURL,
		refreshURL: defaultRefreshURL,
		logger:     log.New(os.Stderr, "[codex] ", log.LstdFlags),
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Name은 provider 식별자를 반환합니다
func (p *CodexProvider) Name() string { return "codex" }

// DisplayName은 UI 표시용 이름을 반환합니다
func (p *CodexProvider) DisplayName() string { return "Codex" }

// AuthMethod는 인증 방식을 반환합니다
func (p *CodexProvider) AuthMethod() provider.AuthMethod { return provider.AuthOAuthFile }

// NeedsAuth는 인증이 필요한 상태인지 반환합니다
func (p *CodexProvider) NeedsAuth() bool {
	token, _, err := p.loadToken(context.Background())
	if err != nil || token == nil {
		return true
	}
	return token.AccessToken == ""
}

// DiscoverCredentials는 로컬 환경에서 Codex OAuth 자격증명을 탐색합니다.
// 탐색 순서:
// 1. CODEX_HOME 환경변수 기반 경로
// 2. ~/.config/codex/auth.json
// 3. ~/.codex/auth.json
// 4. macOS Keychain "Codex Auth"
// 5. credential store (DB)
func (p *CodexProvider) DiscoverCredentials(ctx context.Context) (bool, error) {
	token, _, err := p.loadToken(ctx)
	if err != nil {
		return false, fmt.Errorf("discovering codex credentials: %w", err)
	}
	return token != nil && token.AccessToken != "", nil
}

// RefreshAuth는 last_refresh 기준으로 8일이 지났으면 토큰 갱신을 수행합니다
func (p *CodexProvider) RefreshAuth(ctx context.Context) error {
	token, auth, err := p.loadToken(ctx)
	if err != nil {
		return fmt.Errorf("loading token: %w", err)
	}
	if token == nil {
		return fmt.Errorf("no credentials found: set up Codex CLI (codex login)")
	}
	if auth != nil && needsRefreshByAge(auth.LastRefresh) && token.RefreshToken != "" {
		return p.refreshToken(ctx, token)
	}
	return nil
}

// FetchUsage는 Codex usage API에서 사용량을 조회합니다.
// Header 값을 우선하고, 없으면 body 값을 사용합니다.
// 에러 발생 시 graceful degradation으로 빈 결과를 반환합니다.
func (p *CodexProvider) FetchUsage(ctx context.Context) ([]provider.UsagePoint, error) {
	token, auth, err := p.getValidToken(ctx)
	if err != nil {
		// graceful degradation: 에러 로그 후 빈 결과 반환
		p.logger.Printf("failed to get valid token: %v", err)
		return []provider.UsagePoint{}, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.usageURL, nil)
	if err != nil {
		p.logger.Printf("failed to create usage request: %v", err)
		return []provider.UsagePoint{}, nil
	}

	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "OpenUsage")

	// account_id가 있으면 헤더에 포함
	if auth != nil && auth.Tokens != nil && auth.Tokens.AccountID != "" {
		req.Header.Set("ChatGPT-Account-Id", auth.Tokens.AccountID)
	}

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

	var body usageResponseBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		p.logger.Printf("failed to decode usage response: %v", err)
		return []provider.UsagePoint{}, nil
	}

	rawBytes, _ := json.Marshal(body)
	now := time.Now()
	var points []provider.UsagePoint

	// Header에서 session/weekly 사용률 추출 (우선순위 1)
	sessionPercent := parseHeaderFloat(resp.Header.Get("x-codex-primary-used-percent"))
	weeklyPercent := parseHeaderFloat(resp.Header.Get("x-codex-secondary-used-percent"))
	creditsBalance := parseHeaderFloat(resp.Header.Get("x-codex-credits-balance"))

	// 5시간 세션 사용률 (header 우선, fallback → body)
	if sessionPercent >= 0 {
		limit := 100.0
		var resetAt *time.Time
		if body.RateLimit != nil && body.RateLimit.PrimaryWindow != nil {
			resetAt = parseUnixResetAt(body.RateLimit.PrimaryWindow)
		}
		points = append(points, provider.UsagePoint{
			Metric:      "session",
			Used:        sessionPercent,
			Limit:       &limit,
			ResetAt:     resetAt,
			CollectedAt: now,
			RawJSON:     string(rawBytes),
		})
	} else if body.RateLimit != nil && body.RateLimit.PrimaryWindow != nil {
		limit := 100.0
		resetAt := parseUnixResetAt(body.RateLimit.PrimaryWindow)
		points = append(points, provider.UsagePoint{
			Metric:      "session",
			Used:        body.RateLimit.PrimaryWindow.UsedPercent,
			Limit:       &limit,
			ResetAt:     resetAt,
			CollectedAt: now,
			RawJSON:     string(rawBytes),
		})
	}

	// 7일 주간 사용률 (header 우선, fallback → body)
	if weeklyPercent >= 0 {
		limit := 100.0
		points = append(points, provider.UsagePoint{
			Metric:      "weekly",
			Used:        weeklyPercent,
			Limit:       &limit,
			CollectedAt: now,
		})
	} else if body.RateLimit != nil && body.RateLimit.SecondaryWindow != nil {
		limit := 100.0
		points = append(points, provider.UsagePoint{
			Metric:      "weekly",
			Used:        body.RateLimit.SecondaryWindow.UsedPercent,
			Limit:       &limit,
			CollectedAt: now,
		})
	}

	// 크레딧 잔액 (header 우선, fallback → body)
	if creditsBalance >= 0 {
		limit := creditsMax
		used := creditsMax - creditsBalance
		points = append(points, provider.UsagePoint{
			Metric:      "credits",
			Used:        used,
			Limit:       &limit,
			CollectedAt: now,
		})
	} else if body.Credits != nil {
		limit := creditsMax
		used := creditsMax - body.Credits.Balance
		points = append(points, provider.UsagePoint{
			Metric:      "credits",
			Used:        used,
			Limit:       &limit,
			CollectedAt: now,
		})
	}

	return points, nil
}

// FetchSubscription은 자격증명에서 추출한 구독 정보를 반환합니다
func (p *CodexProvider) FetchSubscription(ctx context.Context) (*provider.SubscriptionInfo, error) {
	return &provider.SubscriptionInfo{
		ProviderName: p.Name(),
		PlanName:     p.planType,
	}, nil
}

// loadToken은 자격증명을 우선순위에 따라 로드합니다:
// 1. CODEX_HOME 환경변수 기반 경로
// 2. ~/.config/codex/auth.json
// 3. ~/.codex/auth.json
// 4. macOS Keychain "Codex Auth" (hex 인코딩 포함)
// 5. credential store (DB)
// codexAuth도 함께 반환해 last_refresh 접근에 사용합니다.
func (p *CodexProvider) loadToken(ctx context.Context) (*oauth.Token, *codexAuth, error) {
	if !p.skipSystemCreds {
		// 1순위: CODEX_HOME 환경변수 기반 경로
		if codexHome := os.Getenv("CODEX_HOME"); codexHome != "" {
			authPath := codexHome + "/auth.json"
			if token, auth, err := readAuthFile(authPath); err == nil && token != nil {
				return token, auth, nil
			}
		}

		// 2순위: ~/.config/codex/auth.json
		if token, auth, err := readAuthFile(credPathConfig); err == nil && token != nil {
			return token, auth, nil
		}

		// 3순위: ~/.codex/auth.json
		if token, auth, err := readAuthFile(credPathLegacy); err == nil && token != nil {
			return token, auth, nil
		}

		// 4순위: macOS Keychain "Codex Auth"
		if raw, err := credfinder.KeychainItem(keychainService, ""); err == nil && raw != "" {
			if token, auth, err := parseAuthPayload(raw); err == nil && token != nil {
				return token, auth, nil
			}
		}
	}

	// 5순위 (또는 skipSystemCreds 시 유일한 소스): credential store (DB)
	if p.credStore != nil {
		if token, err := p.credStore.Get(ctx, p.Name()); err == nil && token != nil {
			return token, nil, nil
		}
	}

	return nil, nil, nil
}

// readAuthFile은 auth.json 파일을 읽어 Token과 codexAuth를 반환합니다
func readAuthFile(path string) (*oauth.Token, *codexAuth, error) {
	var auth codexAuth
	if err := credfinder.ReadJSONCredential(path, &auth); err != nil {
		return nil, nil, err
	}
	token := authToToken(&auth)
	if token == nil {
		return nil, nil, fmt.Errorf("no valid token in auth file")
	}
	return token, &auth, nil
}

// parseAuthPayload는 raw 페이로드(JSON 또는 hex 인코딩)를 파싱합니다.
// Keychain 페이로드가 0x 접두사의 hex 인코딩인 경우를 처리합니다.
func parseAuthPayload(raw string) (*oauth.Token, *codexAuth, error) {
	// hex 인코딩 처리: 0x 접두사 또는 순수 hex 문자열
	jsonData := raw
	if strings.HasPrefix(raw, "0x") || strings.HasPrefix(raw, "0X") {
		decoded, err := hex.DecodeString(raw[2:])
		if err == nil {
			jsonData = string(decoded)
		}
	}

	var auth codexAuth
	if err := json.Unmarshal([]byte(jsonData), &auth); err != nil {
		return nil, nil, fmt.Errorf("parsing keychain payload: %w", err)
	}
	token := authToToken(&auth)
	if token == nil {
		return nil, nil, fmt.Errorf("no valid token in keychain payload")
	}
	return token, &auth, nil
}

// authToToken은 codexAuth를 oauth.Token으로 변환합니다
func authToToken(auth *codexAuth) *oauth.Token {
	if auth == nil || auth.Tokens == nil || auth.Tokens.AccessToken == "" {
		return nil
	}
	return &oauth.Token{
		AccessToken:  auth.Tokens.AccessToken,
		RefreshToken: auth.Tokens.RefreshToken,
	}
}

// getValidToken은 유효한 토큰을 반환합니다.
// last_refresh 기준 8일이 지났으면 선제적으로 갱신합니다.
func (p *CodexProvider) getValidToken(ctx context.Context) (*oauth.Token, *codexAuth, error) {
	token, auth, err := p.loadToken(ctx)
	if err != nil {
		return nil, nil, err
	}
	if token == nil {
		return nil, nil, fmt.Errorf("no credentials available")
	}

	// 선제적 갱신: last_refresh 기준 8일 초과 시
	if auth != nil && needsRefreshByAge(auth.LastRefresh) && token.RefreshToken != "" {
		if refreshErr := p.refreshToken(ctx, token); refreshErr != nil {
			// 갱신 실패 시 현재 토큰으로 계속 시도
			p.logger.Printf("token refresh failed (using current): %v", refreshErr)
		}
	}

	return token, auth, nil
}

// refreshToken은 refresh_token으로 새 access_token을 발급합니다
func (p *CodexProvider) refreshToken(ctx context.Context, token *oauth.Token) error {
	cfg := oauth.OAuth2Config{
		TokenURL: p.refreshURL,
		ClientID: codexOAuthClientID,
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

// needsRefreshByAge는 last_refresh가 8일 이상 지났는지 확인합니다
func needsRefreshByAge(lastRefresh string) bool {
	if lastRefresh == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339, lastRefresh)
	if err != nil {
		// 파싱 실패 시 갱신 필요로 간주
		return true
	}
	return time.Since(t) > refreshAgeDays
}

// parseHeaderFloat는 HTTP 헤더 값을 float64로 변환합니다.
// 헤더가 없거나 파싱 실패 시 -1을 반환합니다 (미설정 sentinel).
func parseHeaderFloat(val string) float64 {
	if val == "" {
		return -1
	}
	f, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return -1
	}
	return f
}

// parseUnixResetAt은 windowData에서 reset 시각을 time.Time으로 변환합니다.
// reset_at Unix 타임스탬프를 우선하고, 없으면 reset_after_seconds를 현재 시각에 더합니다.
func parseUnixResetAt(w *windowData) *time.Time {
	if w == nil {
		return nil
	}
	if w.ResetAt > 0 {
		t := time.Unix(w.ResetAt, 0)
		return &t
	}
	if w.ResetAfterSeconds > 0 {
		t := time.Now().Add(time.Duration(w.ResetAfterSeconds) * time.Second)
		return &t
	}
	return nil
}
