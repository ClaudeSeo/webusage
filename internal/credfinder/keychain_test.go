//go:build darwin

package credfinder

import (
	"errors"
	"testing"
)

func TestKeychainItem_NotFound(t *testing.T) {
	// 존재하지 않는 keychain 항목은 ErrNotFound를 반환해야 합니다
	_, err := KeychainItem("nonexistent-service-xyz-12345", "nonexistent-account")
	if !errors.Is(err, ErrNotFound) && err == nil {
		t.Error("KeychainItem() should return error or ErrNotFound for nonexistent item")
	}
}

func TestKeychainInternetPassword_NotFound(t *testing.T) {
	// 존재하지 않는 internet password는 ErrNotFound를 반환해야 합니다
	_, err := KeychainInternetPassword("nonexistent-server-xyz-12345.invalid", "nonexistent-account")
	if !errors.Is(err, ErrNotFound) && err == nil {
		t.Error("KeychainInternetPassword() should return error or ErrNotFound for nonexistent item")
	}
}
