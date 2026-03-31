package oauth

import "time"

const (
	// expiredBuffer는 토큰 만료 판단 시 사용하는 여유 시간 (5분)
	expiredBuffer = 5 * time.Minute
	// refreshBuffer는 선제적 토큰 갱신 기준 시간 (15분)
	refreshBuffer = 15 * time.Minute
)

// Token은 OAuth2 access/refresh 토큰과 만료 정보를 포함합니다
type Token struct {
	AccessToken  string     `json:"access_token"`
	RefreshToken string     `json:"refresh_token,omitempty"`
	TokenType    string     `json:"token_type,omitempty"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	Scopes       []string   `json:"scopes,omitempty"`
}

// IsExpired는 토큰이 만료되었는지 확인합니다
// 만료 시간 기준 5분 전부터 만료로 판단합니다 (clock skew 보정)
func (t *Token) IsExpired() bool {
	if t.ExpiresAt == nil {
		// 만료 시간 미설정 시 유효한 것으로 간주
		return false
	}
	return time.Now().After(t.ExpiresAt.Add(-expiredBuffer))
}

// NeedsRefresh는 선제적 토큰 갱신이 필요한지 확인합니다
// 만료 15분 전부터 갱신이 필요한 것으로 판단합니다
func (t *Token) NeedsRefresh() bool {
	if t.ExpiresAt == nil {
		return false
	}
	return time.Now().After(t.ExpiresAt.Add(-refreshBuffer))
}

// IsValid는 토큰이 사용 가능한 상태인지 확인합니다
// access_token이 비어있거나 만료된 경우 false 반환
func (t *Token) IsValid() bool {
	return t.AccessToken != "" && !t.IsExpired()
}
