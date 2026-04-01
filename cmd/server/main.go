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
	"github.com/ClaudeSeo/webusage/internal/provider/codex"
	"github.com/ClaudeSeo/webusage/internal/provider/copilot"
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
		"server_host", cfg.ServerHost,
		"server_port", cfg.ServerPort)

	// Initialize database
	s, err := store.NewStore(cfg.DBPath)
	if err != nil {
		logger.Error("Failed to initialize database", "error", err)
		os.Exit(1)
	}
	defer s.Close()

	logger.Info("Database initialized with WAL mode")

	// Setup provider registry + DB 등록 + DB의 enabled 상태 동기화
	registry := provider.NewRegistry()
	count := setupProviders(cfg, registry, s, logger)
	logger.Info("Registered providers", "count", count)

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

	// registry에서 직접 provider 조회 — 런타임 enable/disable 반영
	coll := collector.NewCollector(s, registry, cfg.CollectionInterval, logger)
	httpServer.SetCollector(coll)

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

// setupProviders는 provider 인스턴스를 생성하고 Registry와 DB에 등록합니다.
// DB에 이미 enabled=true인 provider가 있으면 Registry에도 동기화합니다.
// 반환값: 등록된 provider 수
func setupProviders(cfg *config.Config, registry *provider.Registry, s *store.Store, logger *slog.Logger) int {
	providers := []provider.Provider{
		claude.New(),
		codex.New(),
		copilot.New(),
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

	// DB의 enabled 상태를 Registry에 동기화 (서버 재시작 시 활성화 유지)
	dbProviders, err := s.ListProviders()
	if err != nil {
		logger.Error("Failed to list providers for sync", "error", err)
	} else {
		for _, dp := range dbProviders {
			if dp.Enabled {
				if err := registry.SetEnabled(dp.Name, true); err != nil {
					logger.Warn("Failed to sync enabled state", "provider", dp.Name, "error", err)
				} else {
					logger.Info("Restored enabled state from DB", "provider", dp.Name)
				}
			}
		}
	}

	return len(providers)
}
