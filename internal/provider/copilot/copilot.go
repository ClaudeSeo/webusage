package copilot

import (
	"context"
	"encoding/base64"
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
	defaultBaseURL = "https://api.github.com"
)

// copilotUsageResponse는 GitHub Copilot usage API 응답 구조
// GET /copilot_internal/user
type copilotUsageResponse struct {
	// premium_interactions: 프리미엄 상호작용 할당량
	PremiumInteractions *quotaInfo `json:"premium_interactions,omitempty"`
	// chat: 채팅 할당량
	Chat *quotaInfo `json:"chat,omitempty"`
	// next_reset_date: 할당량 초기화 일시
	NextResetDate *string `json:"next_reset_date,omitempty"`
}

type quotaInfo struct {
	Quota int64 `json:"quota"`
	Used  int64 `json:"used"`
}

// CopilotProvider는 GitHub Copilot usage provider
// macOS Keychain의 gh:github.com 항목에서 OAuth 토큰을 탐색합니다
type CopilotProvider struct {
	token      string
	baseURL    string
	httpClient *http.Client
	logger     *log.Logger
}

// Option은 CopilotProvider 설정 함수
type Option func(*CopilotProvider)

// WithBaseURL은 API 기본 URL을 설정합니다 (테스트용)
func WithBaseURL(url string) Option {
	return func(p *CopilotProvider) {
		p.baseURL = url
	}
}

// WithHTTPClient는 HTTP 클라이언트를 설정합니다 (테스트용)
func WithHTTPClient(client *http.Client) Option {
	return func(p *CopilotProvider) {
		p.httpClient = client
	}
}

// WithToken은 OAuth 토큰을 직접 설정합니다 (테스트용)
func WithToken(token string) Option {
	return func(p *CopilotProvider) {
		p.token = token
	}
}

// New는 CopilotProvider 인스턴스를 생성합니다
func New(opts ...Option) *CopilotProvider {
	p := &CopilotProvider{
		baseURL:    defaultBaseURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		logger:     log.New(os.Stderr, "[copilot] ", log.LstdFlags),
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Name은 provider 식별자를 반환합니다
func (p *CopilotProvider) Name() string { return "copilot" }

// DisplayName은 UI 표시용 이름을 반환합니다
func (p *CopilotProvider) DisplayName() string { return "GitHub Copilot" }

// AuthMethod는 Keychain 기반 인증 방식을 반환합니다
func (p *CopilotProvider) AuthMethod() provider.AuthMethod { return provider.AuthKeychain }

// NeedsAuth는 토큰이 없을 때 true를 반환합니다
func (p *CopilotProvider) NeedsAuth() bool { return p.token == "" }

// DiscoverCredentials는 macOS Keychain에서 GitHub OAuth 토큰을 탐색합니다.
// gh CLI가 저장한 github.com / gh:github.com 항목을 탐색합니다.
func (p *CopilotProvider) DiscoverCredentials(_ context.Context) (bool, error) {
	// gh CLI는 base64 인코딩된 토큰을 Keychain internet password로 저장합니다
	raw, err := credfinder.KeychainInternetPassword("github.com", "gh:github.com")
	if err != nil {
		if errors.Is(err, credfinder.ErrNotFound) || errors.Is(err, credfinder.ErrNotSupported) {
			return false, nil
		}
		return false, fmt.Errorf("reading github keychain: %w", err)
	}

	// base64 디코딩: gh CLI는 base64url 형식으로 저장합니다
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		// base64 인코딩이 아닌 경우 raw 값을 그대로 사용
		decoded = []byte(raw)
	}

	token := string(decoded)
	if token == "" {
		return false, nil
	}

	p.token = token
	return true, nil
}

// RefreshAuth는 GitHub OAuth 토큰을 갱신합니다.
// GitHub token은 만료되지 않으므로 재탐색을 수행합니다.
func (p *CopilotProvider) RefreshAuth(ctx context.Context) error {
	found, err := p.DiscoverCredentials(ctx)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("copilot: GitHub credentials not found in keychain")
	}
	return nil
}

// FetchUsage는 GitHub Copilot usage API에서 사용량을 조회합니다.
// 토큰이 없으면 내부에서 DiscoverCredentials를 시도합니다.
func (p *CopilotProvider) FetchUsage(ctx context.Context) ([]provider.UsagePoint, error) {
	// 토큰 없으면 탐색 시도
	if p.token == "" {
		found, err := p.DiscoverCredentials(ctx)
		if err != nil {
			return nil, fmt.Errorf("copilot: credential discovery failed: %w", err)
		}
		if !found {
			return nil, fmt.Errorf("copilot: GitHub credentials not found")
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/copilot_internal/user", nil)
	if err != nil {
		return nil, fmt.Errorf("creating copilot usage request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("copilot usage request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("copilot: GitHub token unauthorized (status 401)")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("copilot usage endpoint returned %d", resp.StatusCode)
	}

	var usageResp copilotUsageResponse
	if err := json.NewDecoder(resp.Body).Decode(&usageResp); err != nil {
		return nil, fmt.Errorf("decoding copilot usage response: %w", err)
	}

	rawBytes, _ := json.Marshal(usageResp)
	now := time.Now()

	var points []provider.UsagePoint

	// premium_interactions 할당량 처리
	if usageResp.PremiumInteractions != nil {
		qi := usageResp.PremiumInteractions
		limit := float64(qi.Quota)
		var resetAt *time.Time
		if usageResp.NextResetDate != nil {
			if t, err := time.Parse(time.RFC3339, *usageResp.NextResetDate); err == nil {
				resetAt = &t
			}
		}
		points = append(points, provider.UsagePoint{
			Metric:      "premium_interactions",
			Used:        float64(qi.Used),
			Limit:       &limit,
			ResetAt:     resetAt,
			CollectedAt: now,
			RawJSON:     string(rawBytes),
		})
	}

	// chat 할당량 처리
	if usageResp.Chat != nil {
		qi := usageResp.Chat
		limit := float64(qi.Quota)
		var resetAt *time.Time
		if usageResp.NextResetDate != nil {
			if t, err := time.Parse(time.RFC3339, *usageResp.NextResetDate); err == nil {
				resetAt = &t
			}
		}
		points = append(points, provider.UsagePoint{
			Metric:      "chat",
			Used:        float64(qi.Used),
			Limit:       &limit,
			ResetAt:     resetAt,
			CollectedAt: now,
			RawJSON:     string(rawBytes),
		})
	}

	return points, nil
}

// FetchSubscription은 GitHub Copilot 구독 정보를 조회합니다
func (p *CopilotProvider) FetchSubscription(ctx context.Context) (*provider.SubscriptionInfo, error) {
	if p.token == "" {
		return nil, fmt.Errorf("copilot: not authenticated")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/copilot_internal/user", nil)
	if err != nil {
		return nil, fmt.Errorf("creating copilot subscription request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("copilot subscription request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("copilot subscription endpoint returned %d", resp.StatusCode)
	}

	var usageResp copilotUsageResponse
	if err := json.NewDecoder(resp.Body).Decode(&usageResp); err != nil {
		return nil, fmt.Errorf("decoding copilot subscription response: %w", err)
	}

	rawBytes, _ := json.Marshal(usageResp)

	return &provider.SubscriptionInfo{
		ProviderName: p.Name(),
		PlanName:     "GitHub Copilot",
		RawJSON:      string(rawBytes),
	}, nil
}
