package provider

import "time"

// AuthMethod는 provider의 인증 방식을 나타냅니다
type AuthMethod string

const (
	// AuthOAuth는 OAuth2 기반 인증
	AuthOAuth AuthMethod = "oauth"
	// AuthOAuthFile은 로컬 파일(~/.xxx/.credentials.json)에서 OAuth 토큰을 읽는 방식
	AuthOAuthFile AuthMethod = "oauth_file"
	// AuthKeychain은 macOS Keychain에서 OAuth 토큰을 읽는 방식
	AuthKeychain AuthMethod = "keychain"
	// AuthLocalDB는 로컬 SQLite/LevelDB에서 OAuth 토큰을 읽는 방식
	AuthLocalDB AuthMethod = "local_db"
)

// UsagePoint는 단일 사용량 측정값
type UsagePoint struct {
	ProviderID  int64      `json:"provider_id"`
	Metric      string     `json:"metric"`
	Used        float64    `json:"used"`
	Limit       *float64   `json:"limit,omitempty"`
	ResetAt     *time.Time `json:"reset_at,omitempty"`
	CollectedAt time.Time  `json:"collected_at"`
	RawJSON     string     `json:"raw_json"`
}

// SubscriptionInfo는 provider의 구독/플랜 정보
type SubscriptionInfo struct {
	// ProviderName은 provider 식별자
	ProviderName string `json:"provider_name"`
	// PlanName은 구독 플랜 이름 (예: "Pro", "Team", "Enterprise")
	PlanName string `json:"plan_name,omitempty"`
	// SubscriptionType은 구독 유형 (예: "free", "paid", "trial")
	SubscriptionType string `json:"subscription_type,omitempty"`
	// RateLimitTier는 속도 제한 등급
	RateLimitTier string `json:"rate_limit_tier,omitempty"`
	// TokenLimit은 월간 토큰 한도 (0이면 무제한 또는 미확인)
	TokenLimit int64 `json:"token_limit,omitempty"`
	// Credits은 남은 크레딧 금액
	Credits float64 `json:"credits,omitempty"`
	// BillingStart은 현재 청구 주기 시작일
	BillingStart *time.Time `json:"billing_start,omitempty"`
	// BillingEnd은 현재 청구 주기 종료일
	BillingEnd *time.Time `json:"billing_end,omitempty"`
	// QuotaSnapshots은 provider별 추가 할당량 스냅샷 데이터
	QuotaSnapshots map[string]interface{} `json:"quota_snapshots,omitempty"`
	// ResetAt은 사용량 초기화 시점
	ResetAt *time.Time `json:"reset_at,omitempty"`
	// RawJSON은 원본 응답 JSON
	RawJSON string `json:"raw_json,omitempty"`
}
