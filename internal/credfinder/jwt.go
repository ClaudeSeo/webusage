package credfinder

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// ParseJWTClaims는 JWT 토큰의 claims를 수동으로 파싱합니다.
// 외부 JWT 라이브러리를 사용하지 않고 base64url 디코딩으로 처리합니다.
// 서명 검증은 하지 않습니다 (로컬 credential discovery 목적).
func ParseJWTClaims(token string) (map[string]interface{}, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid JWT format: expected 3 parts, got %d", len(parts))
	}

	// base64url 패딩 없는 페이로드를 표준 base64로 변환하여 디코딩
	payload := base64urlDecode(parts[1])
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("decoding JWT payload: %w", err)
	}

	var claims map[string]interface{}
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return nil, fmt.Errorf("parsing JWT claims: %w", err)
	}
	return claims, nil
}

// ExtractUserID는 JWT 토큰의 "sub" claim을 추출합니다
func ExtractUserID(token string) (string, error) {
	claims, err := ParseJWTClaims(token)
	if err != nil {
		return "", err
	}

	sub, ok := claims["sub"]
	if !ok {
		return "", fmt.Errorf("JWT missing 'sub' claim")
	}

	subStr, ok := sub.(string)
	if !ok {
		return "", fmt.Errorf("JWT 'sub' claim is not a string")
	}
	return subStr, nil
}

// base64urlDecode는 base64url (패딩 없음)을 표준 base64 (패딩 포함)로 변환합니다.
// RFC 7515: `-` → `+`, `_` → `/`, 필요한 경우 `=` 패딩 추가
func base64urlDecode(s string) string {
	// base64url 문자를 표준 base64로 변환
	s = strings.ReplaceAll(s, "-", "+")
	s = strings.ReplaceAll(s, "_", "/")

	// 4의 배수가 되도록 패딩 추가
	switch len(s) % 4 {
	case 2:
		s += "=="
	case 3:
		s += "="
	}
	return s
}
