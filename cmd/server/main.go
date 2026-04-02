package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ClaudeSeo/webusage/internal/collector"
	"github.com/ClaudeSeo/webusage/internal/config"
	internalhttp "github.com/ClaudeSeo/webusage/internal/http"
	"github.com/ClaudeSeo/webusage/internal/openusage"
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
		"version", "2.0.0", // Major version bump for OpenUsage integration
		"db_path", cfg.DBPath,
		"server_host", cfg.ServerHost,
		"server_port", cfg.ServerPort,
		"openusage_url", cfg.OpenUsageURL)

	// Initialize database
	s, err := store.NewStore(cfg.DBPath)
	if err != nil {
		logger.Error("Failed to initialize database", "error", err)
		os.Exit(1)
	}
	defer s.Close()

	logger.Info("Database initialized with WAL mode")

	// Create OpenUsage client
	client := openusage.NewClient(cfg.OpenUsageURL)

	// Check OpenUsage availability
	if !client.IsHealthy() {
		logger.Warn("OpenUsage API not available at startup",
			"url", cfg.OpenUsageURL,
			"hint", "Make sure OpenUsage app is running")
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

	// Create collector with OpenUsage client
	coll := collector.NewCollector(s, client, cfg.CollectionInterval, logger)
	httpServer.SetCollector(coll)
	httpServer.SetOpenUsageClient(client)

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