package credfinder

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ExpandPath는 경로의 ~ 접두사를 홈 디렉토리로 확장합니다
func ExpandPath(path string) (string, error) {
	if !strings.HasPrefix(path, "~") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("getting home dir: %w", err)
	}
	// ~/... 형식을 홈 디렉토리 기반으로 변환
	return filepath.Join(home, path[1:]), nil
}

// ReadJSONCredential은 JSON 파일을 읽어 dest에 언마샬합니다.
// 경로에서 ~ 확장을 처리합니다.
func ReadJSONCredential(path string, dest interface{}) error {
	expanded, err := ExpandPath(path)
	if err != nil {
		return fmt.Errorf("expanding path %q: %w", path, err)
	}

	data, err := os.ReadFile(expanded)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrNotFound
		}
		return fmt.Errorf("reading credential file %q: %w", expanded, err)
	}

	if err := json.Unmarshal(data, dest); err != nil {
		return fmt.Errorf("parsing credential file %q: %w", expanded, err)
	}
	return nil
}
