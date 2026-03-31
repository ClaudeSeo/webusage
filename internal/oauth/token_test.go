package oauth

import (
	"testing"
	"time"
)

func TestToken_IsExpired(t *testing.T) {
	t.Run("no expiry", func(t *testing.T) {
		token := &Token{AccessToken: "test"}
		if token.IsExpired() {
			t.Error("IsExpired() should be false when ExpiresAt is nil")
		}
	})

	t.Run("expires in 1 hour", func(t *testing.T) {
		exp := time.Now().Add(time.Hour)
		token := &Token{AccessToken: "test", ExpiresAt: &exp}
		if token.IsExpired() {
			t.Error("IsExpired() should be false when token expires in 1 hour")
		}
	})

	t.Run("expired 1 hour ago", func(t *testing.T) {
		exp := time.Now().Add(-time.Hour)
		token := &Token{AccessToken: "test", ExpiresAt: &exp}
		if !token.IsExpired() {
			t.Error("IsExpired() should be true when token expired 1 hour ago")
		}
	})

	t.Run("expires in 3 minutes (within 5min buffer)", func(t *testing.T) {
		exp := time.Now().Add(3 * time.Minute)
		token := &Token{AccessToken: "test", ExpiresAt: &exp}
		// 만료 5분 전부터 expired로 판단
		if !token.IsExpired() {
			t.Error("IsExpired() should be true when token expires within 5min buffer")
		}
	})
}

func TestToken_NeedsRefresh(t *testing.T) {
	t.Run("no expiry", func(t *testing.T) {
		token := &Token{AccessToken: "test"}
		if token.NeedsRefresh() {
			t.Error("NeedsRefresh() should be false when ExpiresAt is nil")
		}
	})

	t.Run("expires in 1 hour", func(t *testing.T) {
		exp := time.Now().Add(time.Hour)
		token := &Token{AccessToken: "test", ExpiresAt: &exp}
		if token.NeedsRefresh() {
			t.Error("NeedsRefresh() should be false when token expires in 1 hour")
		}
	})

	t.Run("expires in 10 minutes (within 15min buffer)", func(t *testing.T) {
		exp := time.Now().Add(10 * time.Minute)
		token := &Token{AccessToken: "test", ExpiresAt: &exp}
		// 만료 15분 전부터 갱신 필요로 판단
		if !token.NeedsRefresh() {
			t.Error("NeedsRefresh() should be true when token expires within 15min buffer")
		}
	})

	t.Run("expires in 20 minutes", func(t *testing.T) {
		exp := time.Now().Add(20 * time.Minute)
		token := &Token{AccessToken: "test", ExpiresAt: &exp}
		if token.NeedsRefresh() {
			t.Error("NeedsRefresh() should be false when token expires in 20 minutes")
		}
	})
}

func TestToken_IsValid(t *testing.T) {
	t.Run("empty access token", func(t *testing.T) {
		token := &Token{}
		if token.IsValid() {
			t.Error("IsValid() should be false when AccessToken is empty")
		}
	})

	t.Run("valid token", func(t *testing.T) {
		exp := time.Now().Add(time.Hour)
		token := &Token{AccessToken: "test", ExpiresAt: &exp}
		if !token.IsValid() {
			t.Error("IsValid() should be true for non-expired token")
		}
	})

	t.Run("expired token", func(t *testing.T) {
		exp := time.Now().Add(-time.Hour)
		token := &Token{AccessToken: "test", ExpiresAt: &exp}
		if token.IsValid() {
			t.Error("IsValid() should be false for expired token")
		}
	})
}
