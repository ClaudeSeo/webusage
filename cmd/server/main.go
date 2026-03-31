package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ClaudeSeo/webusage/internal/collector"
	"github.com/ClaudeSeo/webusage/internal/config"
	internalhttp "github.com/ClaudeSeo/webusage/internal/http"
	"github.com/ClaudeSeo/webusage/internal/provider"
	"github.com/ClaudeSeo/webusage/internal/provider/claude"
	"github.com/ClaudeSeo/webusage/internal/provider/copilot"
	"github.com/ClaudeSeo/webusage/internal/provider/cursor"
	"github.com/ClaudeSeo/webusage/internal/provider/gemini"
	"github.com/ClaudeSeo/webusage/internal/store"
)

func main() {
	// Load configuration
	cfg, err := config.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Setup structured logging (JSON format)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	logger.Info("Starting AI Usage Dashboard",
		"version", "1.0.0",
		"db_path", cfg.DBPath,
		"server_port", cfg.ServerPort)

	// Initialize database
	s, err := store.NewStore(cfg.DBPath)
	if err != nil {
		logger.Error("Failed to initialize database", "error", err)
		os.Exit(1)
	}
	defer s.Close()

	logger.Info("Database initialized with WAL mode")

	// Setup provider registry
	registry := provider.NewRegistry()
	count := setupProviders(cfg, registry, logger)
	if count == 0 {
		// 0개 발견되어도 서버는 시작합니다
		logger.Warn("No providers discovered: dashboard will show no data until credentials are configured")
	}

	// Initialize providers in database
	if err := initializeProvidersInDB(s, registry); err != nil {
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
	httpServer, err := internalhttp.NewServer(s, cfg.ServerHost, cfg.ServerPort, logger)
	if err != nil {
		logger.Error("Failed to create HTTP server", "error", err)
		os.Exit(1)
	}

	// Start collector using registry's enabled providers
	// collector은 RefreshAuth를 호출하지 않습니다.
	// 각 provider의 FetchUsage 내부에서 토큰 갱신을 처리합니다.
	coll := collector.NewCollector(s, registry.ListEnabled(), cfg.CollectionInterval, logger)

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

// setupProviders는 모든 provider를 생성하고 DiscoverCredentials로 자격증명을 탐색합니다.
// 발견된 provider만 Registry에 등록합니다.
// 반환값: 발견된 provider 수
func setupProviders(cfg *config.Config, registry *provider.Registry, logger *slog.Logger) int {
	ctx := context.Background()
	discovered := 0

	// Claude Provider: ~/.claude/.credentials.json 또는 macOS Keychain 자동 탐색
	claudeProvider := claude.New()
	if found, err := claudeProvider.DiscoverCredentials(ctx); err != nil {
		logger.Warn("Claude credential discovery failed", "error", err)
	} else if found {
		registry.Register(claudeProvider)
		logger.Info("Claude provider configured")
		discovered++
	} else {
		logger.Info("Claude credentials not found (run 'claude login' to enable)")
	}

	// Copilot Provider: macOS Keychain (gh CLI) 자동 탐색
	copilotProvider := copilot.New()
	if found, err := copilotProvider.DiscoverCredentials(ctx); err != nil {
		logger.Warn("Copilot credential discovery failed", "error", err)
	} else if found {
		registry.Register(copilotProvider)
		logger.Info("GitHub Copilot provider configured")
		discovered++
	} else {
		logger.Info("GitHub Copilot credentials not found (install gh CLI and run 'gh auth login')")
	}

	// Cursor Provider: SQLite DB 또는 Keychain 자동 탐색
	cursorOpts := []cursor.Option{}
	if cfg.CursorDBPath != "" {
		cursorOpts = append(cursorOpts, cursor.WithDBPath(cfg.CursorDBPath))
	}
	cursorProvider := cursor.New(cursorOpts...)
	if found, err := cursorProvider.DiscoverCredentials(ctx); err != nil {
		logger.Warn("Cursor credential discovery failed", "error", err)
	} else if found {
		registry.Register(cursorProvider)
		logger.Info("Cursor provider configured")
		discovered++
	} else {
		logger.Info("Cursor credentials not found (install Cursor and log in)")
	}

	// Gemini Provider: ~/.gemini/oauth_creds.json 자동 탐색
	geminiOpts := []gemini.Option{}
	if cfg.GeminiCredPath != "" {
		geminiOpts = append(geminiOpts, gemini.WithCredPath(cfg.GeminiCredPath))
	}
	geminiProvider := gemini.New(geminiOpts...)
	if found, err := geminiProvider.DiscoverCredentials(ctx); err != nil {
		logger.Warn("Gemini credential discovery failed", "error", err)
	} else if found {
		registry.Register(geminiProvider)
		logger.Info("Google Gemini provider configured")
		discovered++
	} else {
		logger.Info("Gemini credentials not found (install gemini CLI and log in)")
	}

	logger.Info("Provider discovery complete", "discovered", discovered)
	return discovered
}

// initializeProvidersInDB는 Registry의 provider를 DB에 등록합니다
func initializeProvidersInDB(s *store.Store, registry *provider.Registry) error {
	for _, p := range registry.List() {
		// 이미 존재하는 provider는 건너뜁니다
		existing, err := s.GetProviderByName(p.Name())
		if err == nil && existing != nil {
			continue
		}

		// Provider 설정을 JSON으로 직렬화합니다
		configData := map[string]string{
			"auth_method": string(p.AuthMethod()),
		}
		configJSON, err := json.Marshal(configData)
		if err != nil {
			return fmt.Errorf("marshaling config for %s: %w", p.Name(), err)
		}

		// DB에 provider를 생성합니다
		if _, err := s.CreateProvider(p.Name(), string(configJSON)); err != nil {
			return fmt.Errorf("creating provider %s: %w", p.Name(), err)
		}
	}

	return nil
}
