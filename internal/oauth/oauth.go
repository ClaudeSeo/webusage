package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OAuth2Config는 OAuth2 인증에 필요한 설정값
type OAuth2Config struct {
	ClientID     string
	ClientSecret string
	TokenURL     string
	Scopes       []string
}

// TokenResponse는 OAuth2 토큰 엔드포인트 응답
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

// AuthorizationCodeFlow는 Authorization Code Grant 방식으로 토큰을 교환합니다
func AuthorizationCodeFlow(ctx context.Context, cfg OAuth2Config, code, redirectURI string, client *http.Client) (*Token, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	data := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {redirectURI},
	}
	if cfg.ClientID != "" {
		data.Set("client_id", cfg.ClientID)
	}
	if cfg.ClientSecret != "" {
		data.Set("client_secret", cfg.ClientSecret)
	}

	return doTokenRequest(ctx, cfg.TokenURL, data, client)
}

// DeviceCodeFlow는 Device Authorization Grant 방식으로 토큰을 요청합니다
// deviceCode는 device_code 엔드포인트에서 받은 device_code 값
func DeviceCodeFlow(ctx context.Context, cfg OAuth2Config, deviceCode string, client *http.Client) (*Token, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	data := url.Values{
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		"device_code": {deviceCode},
	}
	if cfg.ClientID != "" {
		data.Set("client_id", cfg.ClientID)
	}

	return doTokenRequest(ctx, cfg.TokenURL, data, client)
}

// RefreshTokenFlow는 refresh_token으로 새 access_token을 발급합니다
func RefreshTokenFlow(ctx context.Context, cfg OAuth2Config, refreshToken string, client *http.Client) (*Token, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	data := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	}
	if cfg.ClientID != "" {
		data.Set("client_id", cfg.ClientID)
	}
	if cfg.ClientSecret != "" {
		data.Set("client_secret", cfg.ClientSecret)
	}

	return doTokenRequest(ctx, cfg.TokenURL, data, client)
}

// doTokenRequest는 토큰 엔드포인트에 POST 요청을 보내고 Token을 반환합니다
func doTokenRequest(ctx context.Context, tokenURL string, data url.Values, client *http.Client) (*Token, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("creating token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token endpoint returned %d", resp.StatusCode)
	}

	var tr TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return nil, fmt.Errorf("decoding token response: %w", err)
	}

	token := &Token{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		TokenType:    tr.TokenType,
	}
	if tr.ExpiresIn > 0 {
		exp := time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
		token.ExpiresAt = &exp
	}

	return token, nil
}
