package config

import (
	"fmt"
	"os"
	"time"

	"github.com/joho/godotenv"
)

// Config는 애플리케이션 전체 설정
type Config struct {
	// DBPath는 SQLite 데이터베이스 파일 경로
	DBPath string
	// ServerPort는 HTTP 서버 포트
	ServerPort int
	// CollectionInterval은 usage 데이터 수집 주기
	CollectionInterval time.Duration

	// Provider credential 경로 설정 (기본값은 각 앱의 표준 위치)
	ClaudeCredPath string // ~/.claude/.credentials.json 기본값
	GeminiCredPath string // ~/.gemini/oauth_creds.json 기본값
	CursorDBPath   string // ~/.cursor/storage 기본값
}

// LoadConfig는 .env 파일과 환경변수에서 설정을 로드합니다
func LoadConfig() (*Config, error) {
	// .env 파일이 없어도 계속 진행
	if err := godotenv.Load(); err != nil {
		fmt.Println("No .env file found, using environment variables")
	}

	interval := getIntEnv("COLLECTION_INTERVAL", 300) // 5분 기본값

	return &Config{
		DBPath:             getEnv("DB_PATH", "./data/usage.db"),
		ServerPort:         getIntEnv("SERVER_PORT", 8080),
		CollectionInterval: time.Duration(interval) * time.Second,
		ClaudeCredPath:     getEnv("CLAUDE_CRED_PATH", "~/.claude/.credentials.json"),
		GeminiCredPath:     getEnv("GEMINI_CRED_PATH", "~/.gemini/oauth_creds.json"),
		CursorDBPath:       getEnv("CURSOR_DB_PATH", ""),
	}, nil
}

// getEnv는 환경변수를 읽고 없으면 기본값을 반환합니다
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getIntEnv는 환경변수를 정수로 읽고 없거나 파싱 실패 시 기본값을 반환합니다
func getIntEnv(key string, defaultValue int) int {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}

	var result int
	if _, err := fmt.Sscanf(value, "%d", &result); err != nil {
		return defaultValue
	}
	return result
}
