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
	"strings"
	"time"

	"github.com/ClaudeSeo/webusage/internal/collector"
	"github.com/ClaudeSeo/webusage/internal/domain"
	"github.com/ClaudeSeo/webusage/internal/openusage"
	"github.com/ClaudeSeo/webusage/internal/store"
)

// Server manages the HTTP server
type Server struct {
	store       *store.Store
	collector   *collector.Collector
	openusage   *openusage.Client
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

// SetCollector sets the collector instance
func (s *Server) SetCollector(c *collector.Collector) {
	s.collector = c
}

// SetOpenUsageClient sets the OpenUsage client
func (s *Server) SetOpenUsageClient(client *openusage.Client) {
	s.openusage = client
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

	heatmap, err := os.ReadFile(filepath.Join(basePath, "components", "heatmap.html"))
	if err != nil {
		return fmt.Errorf("loading heatmap: %w", err)
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
			// KST(Asia/Seoul)로 변환하여 표시
			kst, err := time.LoadLocation("Asia/Seoul")
			if err != nil {
				return t.Format("1/2 15:04")
			}
			return t.In(kst).Format("1/2 15:04")
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
		"isStale": func(t interface{}) bool {
			switch v := t.(type) {
			case *time.Time:
				if v == nil || v.IsZero() {
					return true
				}
				return time.Since(*v) > 2*time.Hour
			case time.Time:
				if v.IsZero() {
					return true
				}
				return time.Since(v) > 2*time.Hour
			default:
				return true
			}
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
		"mulf": func(a, b float64) float64 {
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
		string(providerCard) + string(trendChart) + string(errorState) + string(heatmap)

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

	var views []domain.ProviderView
	for _, p := range providers {
		view := domain.ProviderView{
			ID:        p.ID,
			Name:      p.Name,
			Enabled:   p.Enabled,
			UpdatedAt: p.UpdatedAt,
			LastError: p.LastError,
		}

		cycleConfig := domain.GetProviderCycleConfig(p.Name)
		view.CycleType = string(cycleConfig.CycleType)
		view.LimitType = string(cycleConfig.LimitType)

		snapshots, err := s.store.GetLatestUsageByProvider(p.ID)
		if err == nil && len(snapshots) > 0 {
			now := time.Now()

			primarySnapshot := snapshots[0]
			for _, snap := range snapshots {
				if cycleConfig.CycleType == domain.CycleTypeRolling5h && snap.Metric == "session" {
					primarySnapshot = snap
					break
				}
				if cycleConfig.CycleType == domain.CycleTypeMonthly && (snap.Metric == "premium_interactions" || snap.Metric == "chat") {
					primarySnapshot = snap
					break
				}
			}

			cycleStart, cycleEnd := domain.CalculateCycleBoundaries(cycleConfig.CycleType, now, primarySnapshot.ResetAt)
			if cycleStart != nil {
				view.CycleStartAt = *cycleStart
			}
			if cycleEnd != nil {
				view.CycleEndAt = *cycleEnd
				view.TimeRemaining = domain.FormatDuration(cycleEnd.Sub(now))
			}

			for _, snap := range snapshots {
				mv := domain.MetricView{
					Name:  snap.Metric,
					Label: domain.MetricLabel(snap.Metric),
					Used:  snap.Used,
				}
				if snap.Limit != nil {
					mv.Limit = *snap.Limit
				}
				if mv.Limit > 0 {
					mv.Percent = (mv.Used / mv.Limit) * 100
				}
				if snap.ResetAt != nil {
					mv.ResetAt = *snap.ResetAt
				}
				if snap.CollectedAt.After(view.CollectedAt) {
					view.CollectedAt = snap.CollectedAt
				}
				view.Metrics = append(view.Metrics, mv)
			}
		}

		views = append(views, view)
	}

	data := map[string]interface{}{
		"Providers": views,
		"Year":      time.Now().Year(),
		"Interval":  5,
		"Range":     "5h",
		"TrendData": nil,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "layout", data); err != nil {
		s.logger.Error("Template execution failed", "error", err)
	}
}

// handleProviderAction handles /api/providers/{name}/enable and /api/providers/{name}/disable
func (s *Server) handleProviderAction(w nethttp.ResponseWriter, r *nethttp.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/providers/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 {
		s.jsonError(w, "Invalid path: expected /api/providers/{name}/enable or /api/providers/{name}/disable", nethttp.StatusNotFound)
		return
	}

	name := parts[0]
	action := parts[1]

	if action != "enable" && action != "disable" {
		s.jsonError(w, "Unknown action: use 'enable' or 'disable'", nethttp.StatusNotFound)
		return
	}

	if r.Method != nethttp.MethodPost {
		nethttp.Error(w, "Method not allowed", nethttp.StatusMethodNotAllowed)
		return
	}

	switch action {
	case "enable":
		s.handleEnableProvider(w, r, name)
	case "disable":
		s.handleDisableProvider(w, r, name)
	}
}

// handleEnableProvider activates a provider
func (s *Server) handleEnableProvider(w nethttp.ResponseWriter, r *nethttp.Request, name string) {
	if err := s.store.EnableProviderByName(name, true); err != nil {
		s.jsonError(w, fmt.Sprintf("Failed to enable provider %q: %v", name, err), nethttp.StatusInternalServerError)
		return
	}

	// Trigger immediate collection
	if s.collector != nil {
		go func() {
			if err := s.collector.CollectAll(context.Background()); err != nil {
				s.logger.Error("Immediate collection after enable failed", "provider", name, "error", err)
			}
		}()
	}

	s.jsonResponse(w, map[string]interface{}{
		"provider": name,
		"enabled":  true,
	})
}

// handleDisableProvider deactivates a provider
func (s *Server) handleDisableProvider(w nethttp.ResponseWriter, r *nethttp.Request, name string) {
	if err := s.store.EnableProviderByName(name, false); err != nil {
		s.jsonError(w, fmt.Sprintf("Failed to disable provider %q: %v", name, err), nethttp.StatusInternalServerError)
		return
	}

	s.jsonResponse(w, map[string]interface{}{
		"provider": name,
		"enabled":  false,
	})
}

// handleCollect triggers immediate collection from OpenUsage
func (s *Server) handleCollect(w nethttp.ResponseWriter, r *nethttp.Request) {
	if r.Method != nethttp.MethodPost {
		nethttp.Error(w, "Method not allowed", nethttp.StatusMethodNotAllowed)
		return
	}

	if s.collector == nil {
		s.jsonError(w, "Collector not available", nethttp.StatusInternalServerError)
		return
	}

	go func() {
		if err := s.collector.CollectAll(context.Background()); err != nil {
			s.logger.Error("Manual collection failed", "error", err)
		}
	}()

	s.jsonResponse(w, map[string]interface{}{
		"status":  "collecting",
		"message": "Collection triggered from OpenUsage API",
	})
}

// handleHealthz is the health check endpoint
func (s *Server) handleHealthz(w nethttp.ResponseWriter, r *nethttp.Request) {
	if err := s.store.DB().Ping(); err != nil {
		s.jsonError(w, "Database connection failed", nethttp.StatusServiceUnavailable)
		return
	}

	openusageHealthy := false
	if s.openusage != nil {
		openusageHealthy = s.openusage.IsHealthy()
	}

	s.jsonResponse(w, map[string]interface{}{
		"status":           "healthy",
		"timestamp":        time.Now().Format(time.RFC3339),
		"openusage_status": openusageHealthy,
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

// setupRoutes configures all HTTP routes
func (s *Server) setupRoutes() {
	// Static files
	s.mux.HandleFunc("/static/", s.handleStatic)

	// Dashboard
	s.mux.HandleFunc("/", s.handleDashboard)

	// API endpoints
	s.mux.HandleFunc("/api/current", s.handleAPICurrent)
	s.mux.HandleFunc("/api/trends", s.handleAPITrends)
	s.mux.HandleFunc("/api/forecast", s.handleAPIForecast)
	s.mux.HandleFunc("/api/providers", s.handleAPIProvidersMeta)
	s.mux.HandleFunc("/api/providers/", s.handleProviderAction)
	s.mux.HandleFunc("/api/heatmap", s.handleAPIHeatmap)
	s.mux.HandleFunc("/api/collect", s.handleCollect)
	s.mux.HandleFunc("/healthz", s.handleHealthz)
}

// handleStatic serves static files
func (s *Server) handleStatic(w nethttp.ResponseWriter, r *nethttp.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/static/")
	nethttp.ServeFile(w, r, "static/"+path)
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