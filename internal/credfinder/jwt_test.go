package credfinder

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// makeTestJWT는 테스트용 JWT 토큰을 생성합니다 (서명 없음)
func makeTestJWT(claims map[string]interface{}) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claimsBytes, _ := json.Marshal(claims)
	payload := base64.RawURLEncoding.EncodeToString(claimsBytes)
	return header + "." + payload + ".fakesignature"
}

func TestParseJWTClaims(t *testing.T) {
	t.Run("유효한 JWT 파싱", func(t *testing.T) {
		claims := map[string]interface{}{
			"sub":   "user-123",
			"email": "test@example.com",
		}
		token := makeTestJWT(claims)

		result, err := ParseJWTClaims(token)
		if err != nil {
			t.Fatalf("ParseJWTClaims() error = %v", err)
		}
		if result["sub"] != "user-123" {
			t.Errorf("sub = %v, want %q", result["sub"], "user-123")
		}
		if result["email"] != "test@example.com" {
			t.Errorf("email = %v, want %q", result["email"], "test@example.com")
		}
	})

	t.Run("잘못된 형식 (점 2개)", func(t *testing.T) {
		_, err := ParseJWTClaims("header.payload")
		if err == nil {
			t.Error("ParseJWTClaims() should return error for 2-part token")
		}
	})

	t.Run("base64url 특수문자 포함", func(t *testing.T) {
		// base64url에서 - 와 _ 가 포함된 케이스
		claims := map[string]interface{}{
			"sub": strings.Repeat("a", 10), // 패딩이 필요한 길이
		}
		token := makeTestJWT(claims)
		_, err := ParseJWTClaims(token)
		if err != nil {
			t.Errorf("ParseJWTClaims() error = %v", err)
		}
	})
}

func TestExtractUserID(t *testing.T) {
	t.Run("sub claim이 있는 경우", func(t *testing.T) {
		token := makeTestJWT(map[string]interface{}{
			"sub": "user-abc-123",
		})
		id, err := ExtractUserID(token)
		if err != nil {
			t.Fatalf("ExtractUserID() error = %v", err)
		}
		if id != "user-abc-123" {
			t.Errorf("ExtractUserID() = %q, want %q", id, "user-abc-123")
		}
	})

	t.Run("sub claim이 없는 경우", func(t *testing.T) {
		token := makeTestJWT(map[string]interface{}{
			"email": "test@example.com",
		})
		_, err := ExtractUserID(token)
		if err == nil {
			t.Error("ExtractUserID() should return error when 'sub' claim is missing")
		}
	})

	t.Run("잘못된 JWT", func(t *testing.T) {
		_, err := ExtractUserID("not.a.jwt.token.with.too.many.parts")
		// 파트가 3개 이상이면 JWT 형식 오류
		if err == nil {
			t.Error("ExtractUserID() should return error for malformed JWT")
		}
	})
}
