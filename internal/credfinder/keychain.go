//go:build darwin

package credfinder

import (
	"errors"
	"os/exec"
	"strings"
)

// KeychainItem은 macOS Keychain에서 generic password를 읽습니다.
// account가 비어있으면 -a 플래그를 생략합니다 (Claude Code-credentials 등)
func KeychainItem(service, account string) (string, error) {
	args := []string{"find-generic-password", "-s", service}
	if account != "" {
		args = append(args, "-a", account)
	}
	args = append(args, "-w")
	out, err := exec.Command("security", args...).Output()
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
// account가 비어있으면 -a 플래그를 생략합니다
func KeychainInternetPassword(server, account string) (string, error) {
	args := []string{"find-internet-password", "-s", server}
	if account != "" {
		args = append(args, "-a", account)
	}
	args = append(args, "-w")
	out, err := exec.Command("security", args...).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 44 {
			return "", ErrNotFound
		}
		return "", err
	}
	return strings.TrimRight(string(out), "\n"), nil
}
