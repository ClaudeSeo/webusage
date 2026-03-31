//go:build darwin

package credfinder

import (
	"errors"
	"os/exec"
	"strings"
)

// KeychainItem은 macOS Keychain에서 generic password를 읽습니다.
// `security find-generic-password -s <service> -a <account> -w` 실행
func KeychainItem(service, account string) (string, error) {
	out, err := exec.Command(
		"security", "find-generic-password",
		"-s", service,
		"-a", account,
		"-w",
	).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 44 {
			// exit 44: 항목 없음
			return "", ErrNotFound
		}
		return "", err
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// KeychainInternetPassword는 macOS Keychain에서 internet password를 읽습니다.
// `security find-internet-password -s <server> -a <account> -w` 실행
func KeychainInternetPassword(server, account string) (string, error) {
	out, err := exec.Command(
		"security", "find-internet-password",
		"-s", server,
		"-a", account,
		"-w",
	).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 44 {
			return "", ErrNotFound
		}
		return "", err
	}
	return strings.TrimRight(string(out), "\n"), nil
}
