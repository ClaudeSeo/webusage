package http

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	nethttp "net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/ClaudeSeo/webusage/internal/stats"
	"github.com/ClaudeSeo/webusage/internal/store"
)

// Server manages the HTTP server
type Server struct {
	store       *store.Store
	host        string
	port        int
	logger      *slog.Logger
	mux         *nethttp.ServeMux
	tmpl        *template.Template
	templateDir string
}

// NewServer creates a new HTTP server. templateDir은 옵션 — 빈 문자열이면 "templates" 기본값 사용
func NewServer(s *store.Store, host string, port int, logger *slog.Logger, templateDir ...string) (*Server, error) {
	tdir := "templates"
	if len(templateDir) > 0 && templateDir[0] != "" {
		tdir = templateDir[0]
	}

	server := &Server{
		store:       s,
		host:        host,
		port:        port,
		logger:      logger,
		mux:         nethttp.NewServeMux(),
		templateDir: tdir,
	}

	if err := server.loadTemplates(); err != nil {
		return nil, fmt.Errorf("loading templates: %w", err)
	}

	server.setupRoutes()
	return server, nil
}

// loadTemplates loads HTML templates from the templates/ directory
func (s *Server) loadTemplates() error {
	basePath := s.templateDir

	providerCard, err := os.ReadFile(filepath.Join(basePath, "components", "provider_card.html"))
	if err != nil {
		return fmt.Errorf("loading provider_card: %w", err)
	}

	trendChart, err := os.ReadFile(filepath.Join(basePath, "components", "trend_chart.html"))
	if err != nil {
		return fmt.Errorf("loading trend_chart: %w", err)
	}

	errorState, err := os.ReadFile(filepath.Join(basePath, "components", "error_state.html"))
	if err != nil {
		return fmt.Errorf("loading error_state: %w", err)
	}

	dashboard, err := os.ReadFile(filepath.Join(basePath, "dashboard.html"))
	if err != nil {
		return fmt.Errorf("loading dashboard: %w", err)
	}

	layout, err := os.ReadFile(filepath.Join(basePath, "layout.html"))
	if err != nil {
		return fmt.Errorf("loading layout: %w", err)
	}

	funcMap := template.FuncMap{
		"formatNumber": func(n interface{}) string {
			switch v := n.(type) {
			case int64:
				return fmt.Sprintf("%d", v)
			case float64:
				return fmt.Sprintf("%.0f", v)
			default:
				return fmt.Sprintf("%v", v)
			}
		},
		"formatDateTime": func(t time.Time) string {
			if t.IsZero() {
				return "-"
			}
			return t.Format("1/2 15:04")
		},
		"getUsageClass": func(percentage float64) string {
			if percentage < 50 {
				return "progress-low"
			}
			if percentage < 80 {
				return "progress-medium"
			}
			return "progress-high"
		},
		"isStale": func(t *time.Time) bool {
			if t == nil || t.IsZero() {
				return true
			}
			return time.Since(*t) > 2*time.Hour
		},
		"float64": func(n int64) float64 {
			return float64(n)
		},
		"divf": func(a, b float64) float64 {
			if b == 0 {
				return 0
			}
			return a / b
		},
		"mul": func(a, b int) int {
			return a * b
		},
		"mod": func(a, b int) int {
			return a % b
		},
		"dict": func(values ...interface{}) map[string]interface{} {
			result := make(map[string]interface{})
			for i := 0; i < len(values)-1; i += 2 {
				if key, ok := values[i].(string); ok {
					result[key] = values[i+1]
				}
			}
			return result
		},
	}

	allContent := string(layout) + string(dashboard) +
		string(providerCard) + string(trendChart) + string(errorState)

	tmpl, err := template.New("layout").Funcs(funcMap).Parse(allContent)
	if err != nil {
		return fmt.Errorf("parsing templates: %w", err)
	}

	s.tmpl = tmpl
	return nil
}

// handleDashboard renders the main dashboard page
func (s *Server) handleDashboard(w nethttp.ResponseWriter, r *nethttp.Request) {
	if r.URL.Path != "/" {
		nethttp.NotFound(w, r)
		return
	}

	providers, err := s.store.ListProviders()
	if err != nil {
		s.logger.Error("Failed to list providers", "error", err)
		nethttp.Error(w, "Internal server error", nethttp.StatusInternalServerError)
		return
	}

	type ProviderView struct {
		ID        int64
		Name      string
		Enabled   bool
		Metrics   []struct {
			Metric string
			Used   float64
		}
		LastRun   *time.Time
		LastError *string
	}

	var views []ProviderView
	for _, p := range providers {
		view := ProviderView{
			ID:        p.ID,
			Name:      p.Name,
			Enabled:   p.Enabled,
			LastRun:   p.LastRun,
			LastError: p.LastError,
		}

		// Get latest metrics
		snapshots, err := s.store.GetLatestUsageByProvider(p.ID)
		if err == nil && len(snapshots) > 0 {
			for _, snap := range snapshots {
				view.Metrics = append(view.Metrics, struct {
					Metric string
					Used   float64
				}{snap.Metric, snap.Used})
			}
		}

		views = append(views, view)
	}

	data := map[string]interface{}{
		"Providers": views,
		"Year":      time.Now().Year(),
		"Interval":  5, // Default interval
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "layout", data); err != nil {
		s.logger.Error("Template execution failed", "error", err)
	}
}

// handleCurrentUsage returns latest usage for all providers
func (s *Server) handleCurrentUsage(w nethttp.ResponseWriter, r *nethttp.Request) {
	if r.Method != nethttp.MethodGet {
		nethttp.Error(w, "Method not allowed", nethttp.StatusMethodNotAllowed)
		return
	}

	providers, err := s.store.ListProviders()
	if err != nil {
		s.jsonError(w, "Failed to list providers", nethttp.StatusInternalServerError)
		return
	}

	result := make(map[string]interface{})
	for _, p := range providers {
		snapshots, err := s.store.GetLatestUsageByProvider(p.ID)
		if err != nil {
			continue
		}

		metrics := make(map[string]float64)
		for _, snap := range snapshots {
			metrics[snap.Metric] = snap.Used
		}

		result[p.Name] = map[string]interface{}{
			"provider_id": p.ID,
			"enabled":     p.Enabled,
			"metrics":     metrics,
			"last_run":    p.LastRun,
			"last_error":  p.LastError,
		}
	}

	s.jsonResponse(w, result)
}

// handleTrends returns usage trends over time
func (s *Server) handleTrends(w nethttp.ResponseWriter, r *nethttp.Request) {
	if r.Method != nethttp.MethodGet {
		nethttp.Error(w, "Method not allowed", nethttp.StatusMethodNotAllowed)
		return
	}

	rangeParam := r.URL.Query().Get("range")
	if rangeParam == "" {
		rangeParam = "24h"
	}

	// stats 패키지의 range 검증 + 시간 범위 계산 재사용
	if !stats.IsValidRange(rangeParam) {
		s.jsonError(w, fmt.Sprintf("Invalid range '%s'. Valid values: 24h, 7d, 30d", rangeParam), nethttp.StatusBadRequest)
		return
	}

	tr := stats.GetTimeRange(stats.RangeType(rangeParam))
	startTime, endTime := tr.Start, tr.End

	providers, err := s.store.ListProviders()
	if err != nil {
		s.jsonError(w, "Failed to list providers", nethttp.StatusInternalServerError)
		return
	}

	result := make(map[string]interface{})
	for _, p := range providers {
		snapshots, err := s.store.GetUsageTrends(p.ID, "", startTime, endTime)
		if err != nil {
			continue
		}

		var trendData []map[string]interface{}
		for _, snap := range snapshots {
			trendData = append(trendData, map[string]interface{}{
				"timestamp": snap.CollectedAt,
				"value":     snap.Used,
				"metric":    snap.Metric,
			})
		}

		result[p.Name] = map[string]interface{}{
			"provider_id": p.ID,
			"range":       rangeParam,
			"trend":       trendData,
		}
	}

	s.jsonResponse(w, result)
}

// handleProviders returns list of configured providers
func (s *Server) handleProviders(w nethttp.ResponseWriter, r *nethttp.Request) {
	if r.Method != nethttp.MethodGet {
		nethttp.Error(w, "Method not allowed", nethttp.StatusMethodNotAllowed)
		return
	}

	providers, err := s.store.ListProviders()
	if err != nil {
		s.jsonError(w, "Failed to list providers", nethttp.StatusInternalServerError)
		return
	}

	s.jsonResponse(w, map[string]interface{}{
		"providers": providers,
	})
}

// handleHealthz is the health check endpoint
func (s *Server) handleHealthz(w nethttp.ResponseWriter, r *nethttp.Request) {
	// Check database connection
	if err := s.store.DB().Ping(); err != nil {
		s.jsonError(w, "Database connection failed", nethttp.StatusServiceUnavailable)
		return
	}

	s.jsonResponse(w, map[string]interface{}{
		"status":    "healthy",
		"timestamp": time.Now().Format(time.RFC3339),
	})
}

// jsonResponse sends a JSON response
func (s *Server) jsonResponse(w nethttp.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

// jsonError sends a JSON error response
func (s *Server) jsonError(w nethttp.ResponseWriter, message string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// Start begins serving HTTP requests
func (s *Server) Start(ctx context.Context) error {
	addr := fmt.Sprintf("%s:%d", s.host, s.port)

	server := &nethttp.Server{
		Addr:         addr,
		Handler:      s.mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		<-ctx.Done()
		s.logger.Info("Shutting down HTTP server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		server.Shutdown(shutdownCtx)
	}()

	s.logger.Info("Starting HTTP server", "address", addr)
	if err := server.ListenAndServe(); err != nethttp.ErrServerClosed {
		return err
	}
	return nil
}
