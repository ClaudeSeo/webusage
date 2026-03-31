package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/openclaw/ai-usage-dashboard/internal/collector"
	"github.com/openclaw/ai-usage-dashboard/internal/http"
	"github.com/openclaw/ai-usage-dashboard/internal/provider"
	"github.com/openclaw/ai-usage-dashboard/internal/store"
)

type Config struct {
	DBPath            string
	ServerPort        int
	OpenAIKey         string
	AnthropicKey      string
	CollectionInterval time.Duration
}

func main() {
	// Load environment variables
	if err := godotenv.Load(); err != nil {
		// Continue without .env file if it doesn't exist
		fmt.Println("No .env file found, using environment variables")
	}
	
	// Load configuration
	config := loadConfig()
	
	// Setup structured logging (JSON format)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	
	logger.Info("Starting AI Usage Dashboard",
		"version", "1.0.0",
		"db_path", config.DBPath,
		"server_port", config.ServerPort)
	
	// Initialize database
	store, err := store.NewStore(config.DBPath)
	if err != nil {
		logger.Error("Failed to initialize database", "error", err)
		os.Exit(1)
	}
	defer store.Close()
	
	logger.Info("Database initialized with WAL mode")
	
	// Register providers
	providers := setupProviders(config, logger)
	
	// Initialize providers in database
	if err := initializeProvidersInDB(store, providers); err != nil {
		logger.Error("Failed to initialize providers", "error", err)
		os.Exit(1)
	}
	
	// Create context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	// Setup signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	
	go func() {
		sig := <-sigChan
		logger.Info("Received shutdown signal", "signal", sig)
		cancel()
	}()
	
	// Start HTTP server
	httpServer, err := http.NewServer(store, config.ServerPort, logger)
	if err != nil {
		logger.Error("Failed to create HTTP server", "error", err)
		os.Exit(1)
	}
	
	// Start collector
	coll := collector.NewCollector(store, providers, config.CollectionInterval, logger)
	
	// Run both services
	errChan := make(chan error, 2)
	
	go func() {
		if err := httpServer.Start(ctx); err != nil {
			errChan <- fmt.Errorf("HTTP server: %w", err)
		}
	}()
	
	go func() {
		if err := coll.Start(ctx); err != nil {
			errChan <- fmt.Errorf("Collector: %w", err)
		}
	}()
	
	// Wait for errors or shutdown
	select {
	case err := <-errChan:
		logger.Error("Service failed", "error", err)
		os.Exit(1)
	case <-ctx.Done():
		logger.Info("Shutting down gracefully")
	}
	
	logger.Info("Shutdown complete")
}

func loadConfig() *Config {
	dbPath := getEnv("DB_PATH", "./data/usage.db")
	port := getIntEnv("SERVER_PORT", 8080)
	interval := getIntEnv("COLLECTION_INTERVAL", 300) // 5 minutes default
	
	return &Config{
		DBPath:            dbPath,
		ServerPort:        port,
		OpenAIKey:         os.Getenv("OPENAI_API_KEY"),
		AnthropicKey:      os.Getenv("ANTHROPIC_API_KEY"),
		CollectionInterval: time.Duration(interval) * time.Second,
	}
}

func setupProviders(config *Config, logger *slog.Logger) []provider.Provider {
	var providers []provider.Provider
	
	// OpenAI Provider
	if config.OpenAIKey != "" {
		openaiConfig := provider.ProviderConfig{
			APIKey: config.OpenAIKey,
		}
		providers = append(providers, provider.NewOpenAIProvider(openaiConfig))
		logger.Info("OpenAI provider configured")
	} else {
		logger.Warn("OpenAI API key not configured")
	}
	
	// Anthropic Provider
	if config.AnthropicKey != "" {
		anthropicConfig := provider.ProviderConfig{
			APIKey: config.AnthropicKey,
		}
		providers = append(providers, provider.NewAnthropicProvider(anthropicConfig))
		logger.Info("Anthropic provider configured")
	} else {
		logger.Warn("Anthropic API key not configured")
	}
	
	return providers
}

func initializeProvidersInDB(store *store.Store, providers []provider.Provider) error {
	for _, p := range providers {
		// Check if provider exists
		existing, err := store.GetProviderByName(p.Name())
		if err == nil && existing != nil {
			continue // Already exists
		}
		
		// Create provider config
		config := provider.ProviderConfig{
			APIKey: "***REDACTED***",
		}
		configJSON, err := json.Marshal(config)
		if err != nil {
			return fmt.Errorf("marshaling config for %s: %w", p.Name(), err)
		}
		
		// Insert into database
		if _, err := store.CreateProvider(p.Name(), string(configJSON)); err != nil {
			return fmt.Errorf("creating provider %s: %w", p.Name(), err)
		}
	}
	
	return nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

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
