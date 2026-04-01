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
	"github.com/ClaudeSeo/webusage/internal/provider"
	"github.com/ClaudeSeo/webusage/internal/stats"
	"github.com/ClaudeSeo/webusage/internal/store"
)

// Server manages the HTTP server
type Server struct {
	store       *store.Store
	registry    *provider.Registry
	collector   *collector.Collector
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
		registry:    nil, // SetRegistry로 주입
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

	type MetricView struct {
		Name    string    // 메트릭 키 (session, weekly, credits 등)
		Label   string    // 표시 레이블 (세션 (5h), 주간 (7d) 등)
		Used    float64
		Limit   float64
		Percent float64
		ResetAt time.Time
	}

	type ProviderView struct {
		ID          int64
		Name        string
		Enabled     bool
		Metrics     []MetricView
		CollectedAt time.Time
		UpdatedAt   time.Time
		LastError   *string
	}

	var views []ProviderView
	for _, p := range providers {
		view := ProviderView{
			ID:        p.ID,
			Name:      p.Name,
			Enabled:   p.Enabled,
			UpdatedAt: p.UpdatedAt,
			LastError: p.LastError,
		}

		// 최신 메트릭을 개별 MetricView로 변환
		snapshots, err := s.store.GetLatestUsageByProvider(p.ID)
		if err == nil && len(snapshots) > 0 {
			for _, snap := range snapshots {
				mv := MetricView{
					Name:  snap.Metric,
					Label: metricLabel(snap.Metric),
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
		"Range":     "24h",
		"TrendData": nil,
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

// SetRegistry는 provider Registry를 주입합니다
func (s *Server) SetRegistry(r *provider.Registry) {
	s.registry = r
}

// SetCollector는 Collector를 주입합니다 (즉시 수집, 새로고침 기능)
func (s *Server) SetCollector(c *collector.Collector) {
	s.collector = c
}

// handleProviders returns list of configured providers, including registry metadata if available
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

	type providerResponse struct {
		ID          int64      `json:"id"`
		Name        string     `json:"name"`
		DisplayName string     `json:"display_name"`
		AuthMethod  string     `json:"auth_method"`
		Enabled     bool       `json:"enabled"`
		LastRun     *time.Time `json:"last_run,omitempty"`
		LastError   *string    `json:"last_error,omitempty"`
	}

	result := make([]providerResponse, 0, len(providers))
	for _, p := range providers {
		resp := providerResponse{
			ID:          p.ID,
			Name:        p.Name,
			DisplayName: p.Name,
			Enabled:     p.Enabled,
			LastRun:     p.LastRun,
			LastError:   p.LastError,
		}

		// registry에서 display_name, auth_method, enabled 상태 보강
		if s.registry != nil {
			if rp, ok := s.registry.Get(p.Name); ok {
				resp.DisplayName = rp.DisplayName()
				resp.AuthMethod = string(rp.AuthMethod())
				resp.Enabled = s.registry.IsEnabled(p.Name)
			}
		}

		result = append(result, resp)
	}

	s.jsonResponse(w, map[string]interface{}{
		"providers": result,
	})
}

// handleProviderAction handles /api/providers/{name}/enable and /api/providers/{name}/disable
func (s *Server) handleProviderAction(w nethttp.ResponseWriter, r *nethttp.Request) {
	// URL: /api/providers/{name}/enable 또는 /api/providers/{name}/disable
	// path prefix "/api/providers/" 이후 파싱
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

// handleEnableProvider activates a provider after credential discovery
func (s *Server) handleEnableProvider(w nethttp.ResponseWriter, r *nethttp.Request, name string) {
	if s.registry == nil {
		s.jsonError(w, "Registry not available", nethttp.StatusInternalServerError)
		return
	}

	p, ok := s.registry.Get(name)
	if !ok {
		s.jsonError(w, fmt.Sprintf("Provider %q not found. Available providers: claude, copilot, cursor, gemini", name), nethttp.StatusBadRequest)
		return
	}

	found, err := p.DiscoverCredentials(r.Context())
	if err != nil {
		s.jsonError(w, fmt.Sprintf("Credential discovery failed for %q: %v", name, err), nethttp.StatusBadRequest)
		return
	}
	if !found {
		s.jsonError(w, fmt.Sprintf("No credentials found for %q. Please log in to the provider first.", name), nethttp.StatusBadRequest)
		return
	}

	if err := s.store.EnableProviderByName(name, true); err != nil {
		s.jsonError(w, fmt.Sprintf("Failed to enable provider %q in database: %v", name, err), nethttp.StatusInternalServerError)
		return
	}

	if err := s.registry.SetEnabled(name, true); err != nil {
		s.logger.Warn("Failed to set registry enabled state", "provider", name, "error", err)
	}

	// 활성화 직후 즉시 수집 트리거
	if s.collector != nil {
		go func() {
			if err := s.collector.CollectSingle(context.Background(), name); err != nil {
				s.logger.Warn("Immediate collection after enable failed", "provider", name, "error", err)
			}
		}()
	}

	dbProvider, err := s.store.GetProviderByName(name)
	if err != nil {
		s.jsonError(w, "Provider enabled but failed to fetch updated state", nethttp.StatusInternalServerError)
		return
	}

	s.jsonResponse(w, map[string]interface{}{
		"provider":     dbProvider,
		"display_name": p.DisplayName(),
		"auth_method":  string(p.AuthMethod()),
		"enabled":      true,
	})
}

// handleDisableProvider deactivates a provider
func (s *Server) handleDisableProvider(w nethttp.ResponseWriter, r *nethttp.Request, name string) {
	if err := s.store.EnableProviderByName(name, false); err != nil {
		s.jsonError(w, fmt.Sprintf("Failed to disable provider %q: %v", name, err), nethttp.StatusInternalServerError)
		return
	}

	if s.registry != nil {
		s.registry.SetEnabled(name, false) //nolint:errcheck — provider 미존재 시 무시
	}

	s.jsonResponse(w, map[string]interface{}{
		"name":    name,
		"enabled": false,
	})
}

// handleCollect는 모든 enabled provider의 즉시 수집을 트리거합니다 (새로고침 버튼용)
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
		"message": "Collection triggered for all enabled providers",
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

// metricLabels는 메트릭 키 → 한글 표시 레이블 매핑 (읽기 전용, 동시 접근 안전)
var metricLabels = map[string]string{
	"session":              "세션 (5h)",
	"weekly":               "주간 (7d)",
	"weekly_sonnet":        "주간 Sonnet",
	"extra_credits":        "Extra 크레딧",
	"credits":              "크레딧",
	"requests_total":       "총 요청",
	"requests_fast":        "프리미엄 요청",
	"premium_interactions": "프리미엄 사용량",
	"chat":                 "채팅",
	"api_access":           "API 접근",
}

// metricLabel은 메트릭 키를 한글 표시 레이블로 변환합니다
func metricLabel(metric string) string {
	if label, ok := metricLabels[metric]; ok {
		return label
	}
	return metric
}
