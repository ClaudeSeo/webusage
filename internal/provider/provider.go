package provider

import "context"

// Provider는 AI usage provider의 공통 인터페이스
type Provider interface {
	// Name은 provider 식별자 반환 (예: "claude", "openai")
	Name() string

	// DisplayName은 UI에 표시할 이름 반환 (예: "Claude", "OpenAI")
	DisplayName() string

	// AuthMethod는 provider의 인증 방식 반환
	AuthMethod() AuthMethod

	// NeedsAuth는 인증이 필요한 상태인지 반환
	// (자격증명 미설정 또는 토큰 만료 시 true)
	NeedsAuth() bool

	// DiscoverCredentials는 로컬 환경에서 OAuth 자격증명을 자동 탐색합니다.
	// 반환값: (발견 여부, 에러)
	DiscoverCredentials(ctx context.Context) (bool, error)

	// RefreshAuth는 기존 토큰 갱신을 수행합니다
	RefreshAuth(ctx context.Context) error

	// FetchUsage는 provider에서 사용량 데이터 수집
	// collector 호환성 유지를 위해 기존 시그니처 보존
	FetchUsage(ctx context.Context) ([]UsagePoint, error)

	// FetchSubscription은 provider의 구독/플랜 정보 조회
	FetchSubscription(ctx context.Context) (*SubscriptionInfo, error)
}
