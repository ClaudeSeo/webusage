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
	DemoMode          bool
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
	
	// Seed demo data if DEMO_MODE is enabled
	if config.DemoMode {
		logger.Info("DEMO_MODE enabled - seeding sample data")
		if err := seedDemoData(store); err != nil {
			logger.Error("Failed to seed demo data", "error", err)
			os.Exit(1)
		}
		logger.Info("Demo data seeded successfully")
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
	demoMode := getEnv("DEMO_MODE", "false") == "true"
	
	return &Config{
		DBPath:            dbPath,
		ServerPort:        port,
		OpenAIKey:         os.Getenv("OPENAI_API_KEY"),
		AnthropicKey:      os.Getenv("ANTHROPIC_API_KEY"),
		CollectionInterval: time.Duration(interval) * time.Second,
		DemoMode:          demoMode,
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

// seedDemoData inserts sample provider and usage data for demo purposes
func seedDemoData(s *store.Store) error {
	now := time.Now()
	resetAt := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	
	// Seed providers (ignore if already exist)
	providerIDs := make(map[string]int64)
	for _, p := range []struct{ id, name string }{
		{"openai", "OpenAI"},
		{"anthropic", "Anthropic"},
	} {
		id, err := s.CreateProvider(p.name, ``)
		if err != nil {
			// Try to get existing provider
			existing, err := s.GetProviderByName(p.name)
			if err != nil {
				return fmt.Errorf("creating provider %s: %w", p.name, err)
			}
			id = existing.ID
		}
		providerIDs[p.id] = id
	}
	
	// Seed usage snapshots (24h history)
	limitOpenAI := float64(100000)
	limitAnthropic := float64(100000)
	
	for i := 24; i >= 0; i-- {
		t := now.Add(-time.Duration(i) * time.Hour)
		
		// OpenAI: 45% usage trend
		_, err := s.CreateUsageSnapshot(&store.UsageSnapshot{
			ProviderID:  providerIDs["openai"],
			Metric:      "usage",
			Used:        float64(45000 - i*500),
			Limit:       &limitOpenAI,
			ResetAt:     &resetAt,
			CollectedAt: t,
			RawJSON:     `{"tokens": 45000}`,
		})
		if err != nil {
			return fmt.Errorf("creating OpenAI snapshot: %w", err)
		}
		
		// Anthropic: 82% usage trend
		_, err = s.CreateUsageSnapshot(&store.UsageSnapshot{
			ProviderID:  providerIDs["anthropic"],
			Metric:      "usage",
			Used:        float64(82000 - i*300),
			Limit:       &limitAnthropic,
			ResetAt:     &resetAt,
			CollectedAt: t,
			RawJSON:     `{"tokens": 82000}`,
		})
		if err != nil {
			return fmt.Errorf("creating Anthropic snapshot: %w", err)
		}
	}
	
	return nil
}
