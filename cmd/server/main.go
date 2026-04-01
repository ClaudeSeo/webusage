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

	// Setup provider registry — 모든 provider 등록 (기본 disabled)
	registry := provider.NewRegistry()
	count := setupProviders(cfg, registry, s, logger)
	logger.Info("Registered providers (all disabled by default)", "count", count)

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
	httpServer.SetRegistry(registry)

	// collector는 DB의 enabled 상태를 기준으로 수집 — registry는 인증 에러 시 런타임 비활성화에 사용
	coll := collector.NewCollector(s, registry.ListEnabled(), cfg.CollectionInterval, logger, registry)

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

// setupProviders는 4개 provider 인스턴스를 생성하고 Registry와 DB에 모두 등록합니다.
// DiscoverCredentials를 호출하지 않습니다 — 자격증명 탐색은 provider 활성화 시 수행됩니다.
// 반환값: 등록된 provider 수
func setupProviders(cfg *config.Config, registry *provider.Registry, s *store.Store, logger *slog.Logger) int {
	// Cursor, Gemini는 option 인자를 받으므로 먼저 구성합니다
	cursorOpts := []cursor.Option{}
	if cfg.CursorDBPath != "" {
		cursorOpts = append(cursorOpts, cursor.WithDBPath(cfg.CursorDBPath))
	}

	geminiOpts := []gemini.Option{}
	if cfg.GeminiCredPath != "" {
		geminiOpts = append(geminiOpts, gemini.WithCredPath(cfg.GeminiCredPath))
	}

	providers := []provider.Provider{
		claude.New(),
		copilot.New(),
		cursor.New(cursorOpts...),
		gemini.New(geminiOpts...),
	}

	for _, p := range providers {
		// Registry에 등록 (enabled=false 기본값)
		registry.Register(p)

		// DB에 비활성화 상태로 등록 (INSERT OR IGNORE — 이미 존재하면 무시)
		configData := map[string]string{
			"auth_method": string(p.AuthMethod()),
		}
		configJSON, err := json.Marshal(configData)
		if err != nil {
			logger.Error("Failed to marshal provider config", "provider", p.Name(), "error", err)
			continue
		}

		if _, err := s.CreateProviderDisabled(p.Name(), p.Name(), string(configJSON)); err != nil {
			logger.Error("Failed to register provider in DB", "provider", p.Name(), "error", err)
		}
	}

	return len(providers)
}
