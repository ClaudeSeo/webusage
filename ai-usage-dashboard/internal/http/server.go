package http

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"time"

	"github.com/openclaw/ai-usage-dashboard/internal/store"
)

// Server manages the HTTP server
type Server struct {
	store   *store.Store
	port    int
	logger  *slog.Logger
	mux     *http.ServeMux
	tmpl    *template.Template
}

// NewServer creates a new HTTP server
func NewServer(s *store.Store, port int, logger *slog.Logger) (*Server, error) {
	server := &Server{
		store:  s,
		port:   port,
		logger: logger,
		mux:    http.NewServeMux(),
	}
	
	if err := server.loadTemplates(); err != nil {
		return nil, fmt.Errorf("loading templates: %w", err)
	}
	
	server.setupRoutes()
	return server, nil
}

// loadTemplates loads HTML templates for SSR
func (s *Server) loadTemplates() error {
	// Define inline templates for simplicity
	tmplStr := `
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>AI Usage Dashboard</title>
    <style>
        * { box-sizing: border-box; margin: 0; padding: 0; }
        body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; background: #f5f5f7; color: #1d1d1f; padding: 20px; }
        .container { max-width: 1200px; margin: 0 auto; }
        h1 { margin-bottom: 20px; font-size: 28px; }
        .grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(300px, 1fr)); gap: 20px; margin-bottom: 30px; }
        .card { background: white; border-radius: 12px; padding: 20px; box-shadow: 0 2px 8px rgba(0,0,0,0.1); }
        .card h2 { font-size: 18px; margin-bottom: 15px; color: #6e6e73; }
        .metric { display: flex; justify-content: space-between; padding: 10px 0; border-bottom: 1px solid #e5e5e5; }
        .metric:last-child { border-bottom: none; }
        .metric-label { color: #6e6e73; }
        .metric-value { font-weight: 600; }
        .status { display: inline-block; padding: 4px 12px; border-radius: 20px; font-size: 12px; font-weight: 600; }
        .status-ok { background: #d4edda; color: #155724; }
        .status-error { background: #f8d7da; color: #721c24; }
        .error-msg { color: #dc3545; font-size: 13px; margin-top: 8px; }
        .chart-container { height: 200px; margin-top: 15px; }
        footer { text-align: center; padding: 20px; color: #6e6e73; font-size: 13px; }
    </style>
</head>
<body>
    <div class="container">
        <h1>📊 AI Usage Dashboard</h1>
        
        <div class="grid">
            {{range .Providers}}
            <div class="card">
                <h2>{{.Name}} {{if .Enabled}}<span class="status status-ok">Active</span>{{else}}<span class="status status-error">Disabled</span>{{end}}</h2>
                {{range .Metrics}}
                <div class="metric">
                    <span class="metric-label">{{.Metric}}</span>
                    <span class="metric-value">{{printf "%.2f" .Used}}</span>
                </div>
                {{end}}
                {{if .LastError}}
                <div class="error-msg">⚠️ {{.LastError}}</div>
                {{end}}
                <div style="font-size: 12px; color: #6e6e73; margin-top: 10px;">
                    Last updated: {{if .LastRun}}{{.LastRun.Format "2006-01-02 15:04"}}{{else}}Never{{end}}
                </div>
            </div>
            {{end}}
        </div>
        
        <footer>
            OpenUsage Dashboard &copy; {{.Year}} | Data refreshes every {{.Interval}} minutes
        </footer>
    </div>
</body>
</html>
`
	
	var err error
	s.tmpl, err = template.New("dashboard").Parse(tmplStr)
	return err
}

// setupRoutes configures HTTP routes
func (s *Server) setupRoutes() {
	// API endpoints
	s.mux.HandleFunc("/api/current", s.handleCurrentUsage)
	s.mux.HandleFunc("/api/trends", s.handleTrends)
	s.mux.HandleFunc("/api/providers", s.handleProviders)
	s.mux.HandleFunc("/healthz", s.handleHealthz)
	
	// Web UI
	s.mux.HandleFunc("/", s.handleDashboard)
}

// handleDashboard renders the main dashboard page
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	
	providers, err := s.store.ListProviders()
	if err != nil {
		s.logger.Error("Failed to list providers", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
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
			ID:      p.ID,
			Name:    p.Name,
			Enabled: p.Enabled,
			LastRun: p.LastRun,
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
	if err := s.tmpl.Execute(w, data); err != nil {
		s.logger.Error("Template execution failed", "error", err)
	}
}

// handleCurrentUsage returns latest usage for all providers
func (s *Server) handleCurrentUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	
	providers, err := s.store.ListProviders()
	if err != nil {
		s.jsonError(w, "Failed to list providers", http.StatusInternalServerError)
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
func (s *Server) handleTrends(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	
	rangeParam := r.URL.Query().Get("range")
	if rangeParam == "" {
		rangeParam = "24h"
	}
	
	// Validate range parameter
	if !isValidRange(rangeParam) {
		s.jsonError(w, fmt.Sprintf("Invalid range '%s'. Valid values: 24h, 7d, 30d", rangeParam), http.StatusBadRequest)
		return
	}
	
	// Parse range
	var duration time.Duration
	switch rangeParam {
	case "24h":
		duration = 24 * time.Hour
	case "7d":
		duration = 7 * 24 * time.Hour
	case "30d":
		duration = 30 * 24 * time.Hour
	}
	
	endTime := time.Now()
	startTime := endTime.Add(-duration)
	
	providers, err := s.store.ListProviders()
	if err != nil {
		s.jsonError(w, "Failed to list providers", http.StatusInternalServerError)
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

// isValidRange checks if the range parameter is valid
func isValidRange(rangeStr string) bool {
	switch rangeStr {
	case "24h", "7d", "30d":
		return true
	default:
		return false
	}
}

// handleProviders returns list of configured providers
func (s *Server) handleProviders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	
	providers, err := s.store.ListProviders()
	if err != nil {
		s.jsonError(w, "Failed to list providers", http.StatusInternalServerError)
		return
	}
	
	s.jsonResponse(w, map[string]interface{}{
		"providers": providers,
	})
}

// handleHealthz is the health check endpoint
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	// Check database connection
	if err := s.store.DB().Ping(); err != nil {
		s.jsonError(w, "Database connection failed", http.StatusServiceUnavailable)
		return
	}
	
	s.jsonResponse(w, map[string]interface{}{
		"status":    "healthy",
		"timestamp": time.Now().Format(time.RFC3339),
	})
}

// jsonResponse sends a JSON response
func (s *Server) jsonResponse(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

// jsonError sends a JSON error response
func (s *Server) jsonError(w http.ResponseWriter, message string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// Start begins serving HTTP requests
func (s *Server) Start(ctx context.Context) error {
	addr := fmt.Sprintf(":%d", s.port)
	
	server := &http.Server{
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
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}
