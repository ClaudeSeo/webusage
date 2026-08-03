package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config is the application-wide configuration
type Config struct {
	// DBPath is the SQLite database file path
	DBPath string
	// ServerHost is the HTTP server bind address (default: 127.0.0.1)
	ServerHost string
	// ServerPort is the HTTP server port
	ServerPort int
	// CollectionInterval is the usage data collection interval
	CollectionInterval time.Duration

	// OpenUsageURL is the OpenUsage API endpoint
	OpenUsageURL string

	// OpenUsageEnabled disables the OpenUsage HTTP collection path when false.
	// Set to false when using only native providers (without the OpenUsage app).
	OpenUsageEnabled bool

	// OllamaAPIKey authenticates the Ollama Cloud usage API. The Ollama native
	// provider has no local state to discover, so an empty value is what marks
	// it unavailable. Treat as a secret: never log or serialize it.
	OllamaAPIKey string

	// Title is the site title suffix. When non-empty, "WebUsage - <Title>" is displayed
	Title string
}

// LoadConfig loads configuration from the .env file and environment variables
func LoadConfig() (*Config, error) {
	// Continue even if the .env file is missing
	if err := godotenv.Load(); err != nil {
		fmt.Println("No .env file found, using environment variables")
	}

	interval := getIntEnv("COLLECTION_INTERVAL", 900) // 15-minute default

	return &Config{
		DBPath:             getEnv("DB_PATH", "./data/usage.db"),
		ServerHost:         getEnv("SERVER_HOST", "127.0.0.1"),
		ServerPort:         getIntEnv("SERVER_PORT", 8080),
		CollectionInterval: time.Duration(interval) * time.Second,
		OpenUsageURL:       getEnv("OPENUSAGE_URL", "http://127.0.0.1:6736"),
		OpenUsageEnabled:   getBoolEnv("OPENUSAGE_ENABLED", true),
		OllamaAPIKey:       getEnv("OLLAMA_API_KEY", ""),
		Title:              getEnv("TITLE", ""),
	}, nil
}

// getEnv reads an environment variable, returning the default value if unset
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getIntEnv reads an environment variable as an integer, returning the default on absence or parse failure
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

// getBoolEnv reads an environment variable as a boolean. "1", "true", "yes" (case-insensitive) are
// true, other non-empty values are false, and an empty value returns defaultValue.
func getBoolEnv(key string, defaultValue bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	switch strings.ToLower(value) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return defaultValue
	}
}
