//go:build !darwin

package credfinder

// KeychainItem은 macOS 전용 함수입니다. non-darwin에서는 ErrNotSupported를 반환합니다.
func KeychainItem(service, account string) (string, error) {
	return "", ErrNotSupported
}

// KeychainInternetPassword는 macOS 전용 함수입니다. non-darwin에서는 ErrNotSupported를 반환합니다.
func KeychainInternetPassword(server, account string) (string, error) {
	return "", ErrNotSupported
}
