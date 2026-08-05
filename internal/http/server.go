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
	"sync"
	"time"

	"github.com/ClaudeSeo/webusage/internal/collector"
	"github.com/ClaudeSeo/webusage/internal/domain"
	"github.com/ClaudeSeo/webusage/internal/openusage"
	"github.com/ClaudeSeo/webusage/internal/store"
)

// staleUsageThreshold is how long a provider's newest snapshot may age before
// the dashboard reports its data as stale. The server renders the first paint
// and the browser re-evaluates it after each refresh, so both read this value.
const staleUsageThreshold = 2 * time.Hour

// Server manages the HTTP server
type Server struct {
	store              *store.Store
	collector          *collector.Collector
	openusage          *openusage.Client
	host               string
	port               int
	logger             *slog.Logger
	mux                *nethttp.ServeMux
	tmpl               *template.Template
	templateDir        string
	title              string
	collectionInterval time.Duration
	collectionMu       sync.RWMutex
	collectionRuns     map[string]*collectionRun
	nextCollectionID   uint64
	latestCollectionID string
}

// collectionRun records the lifecycle of one asynchronous manual collection.
// Raw collector errors are intentionally not retained because this state is
// exposed through an HTTP status resource.
type collectionRun struct {
	ID              string
	Status          string
	Terminal        bool
	StartedAt       time.Time
	CompletedAt     time.Time
	CollectionError *string
}

// SSRMetricView is the server-side summary consumed by the dashboard's first
// paint. It embeds the legacy metric fields and adds the deterministic G1
// projection values so a client render can hydrate without fixture data.
type SSRMetricView struct {
	domain.MetricView
	Metric                 string                `json:"metric"`
	CycleType              string                `json:"cycle_type"`
	CycleLabel             string                `json:"cycle_label"`
	CurrentPercent         float64               `json:"current_percent"`
	PacePerHour            float64               `json:"pace_per_hour"`
	ProjectedUsage         float64               `json:"projected_usage"`
	ProjectedPercent       float64               `json:"projected_percent"`
	HasProjection          bool                  `json:"has_projection"`
	HasForecast            bool                  `json:"has_forecast"`
	ForecastUsable         bool                  `json:"forecast_usable"`
	WeakEstimate           bool                  `json:"weak_estimate"`
	Severity               domain.MetricSeverity `json:"severity"`
	TimeRemaining          string                `json:"time_remaining,omitempty"`
	ObservationWindowHours float64               `json:"observation_window_hours,omitempty"`
}

// SSRProviderView keeps ProviderView's public fields while exposing metric
// projections as a separate collection. Existing templates can continue using
// Name/Enabled/LastError/etc. through the embedded legacy view.
type SSRProviderView struct {
	domain.ProviderView
	Metrics []SSRMetricView `json:"metrics"`
	// DisplayCycleType is the cycle shown in the provider card description. The
	// provider-wide configuration can name a headline metric the provider no
	// longer reports, so the first rendered metric's cycle is preferred and the
	// configured cycle is only a fallback for providers without metrics.
	DisplayCycleType  string                            `json:"display_cycle_type,omitempty"`
	PrimaryMetric     string                            `json:"primary_metric,omitempty"`
	MetricProjections map[string]map[string]interface{} `json:"metric_projections,omitempty"`
}

// NewServer creates a new HTTP server. templateDir is optional — defaults to "templates" when empty
func NewServer(s *store.Store, host string, port int, logger *slog.Logger, templateDir ...string) (*Server, error) {
	tdir := "templates"
	if len(templateDir) > 0 && templateDir[0] != "" {
		tdir = templateDir[0]
	}

	server := &Server{
		store:              s,
		host:               host,
		port:               port,
		logger:             logger,
		mux:                nethttp.NewServeMux(),
		templateDir:        tdir,
		collectionInterval: 15 * time.Minute,
		collectionRuns:     make(map[string]*collectionRun),
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

// SetTitle sets the site title suffix
func (s *Server) SetTitle(title string) {
	s.title = title
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
			// Convert to KST (Asia/Seoul) for display
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
				return time.Since(*v) > staleUsageThreshold
			case time.Time:
				if v.IsZero() {
					return true
				}
				return time.Since(v) > staleUsageThreshold
			default:
				return true
			}
		},
		// The dashboard re-evaluates staleness client-side after every refresh,
		// so the threshold is published instead of duplicated in the template's
		// script.
		"staleThresholdSeconds": func() int64 { return int64(staleUsageThreshold / time.Second) },
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
		// hatchTone selects the projection hatch colour token for a severity so
		// the projected-overshoot band reads in the same tone as its gauge fill.
		"hatchTone": func(severity domain.MetricSeverity) string {
			switch severity {
			case domain.MetricSeverityDanger:
				return "var(--danger)"
			case domain.MetricSeverityWarn:
				return "var(--warn)"
			default:
				return "var(--fg)"
			}
		},
		// cycleShort is the compact cycle label used inside provider card metric
		// rows, where the full label would dominate the metric name.
		"cycleShort": func(cycleType string) string {
			return domain.CycleShortLabel(domain.CycleType(cycleType))
		},
		// cycleLabel and limitLabel keep provider-level cycle and limit copy in
		// Korean, matching the metric badges rendered below them.
		"cycleLabel": func(cycleType string) string {
			return domain.CycleLabel(domain.CycleType(cycleType))
		},
		"limitLabel": func(limitType string) string {
			return domain.LimitTypeLabel(domain.LimitType(limitType))
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

	var legacyViews []domain.ProviderView
	for _, p := range providers {
		view := domain.ProviderView{
			ID:        p.ID,
			Name:      p.Name,
			Enabled:   p.Enabled,
			UpdatedAt: p.UpdatedAt,
			LastError: safeCollectionError(p.LastError),
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

			metricViews := make(map[string]domain.MetricView, len(snapshots))
			catalog := make([]string, 0, len(snapshots))
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
				catalog = append(catalog, snap.Metric)
				metricViews[snap.Metric] = mv
			}

			preference, err := s.store.GetMetricPreference(p.ID)
			if err != nil {
				s.logger.Error("Failed to load dashboard metric preference", "provider", p.Name, "error", err)
				nethttp.Error(w, "Internal server error", nethttp.StatusInternalServerError)
				return
			}
			for _, item := range domain.ReconcileMetricPreferences(preference.Items, catalog) {
				if !item.Available || !item.Visible {
					continue
				}
				view.Metrics = append(view.Metrics, metricViews[item.Metric])
			}
		}

		legacyViews = append(legacyViews, view)
	}

	views := make([]SSRProviderView, 0, len(legacyViews))
	for _, legacy := range legacyViews {
		ssr := s.buildSSRProviderView(legacy, time.Now().UTC())
		views = append(views, ssr)
	}

	data := map[string]interface{}{
		"Providers":                 views,
		"Year":                      time.Now().Year(),
		"Interval":                  int(s.currentCollectionInterval() / time.Minute),
		"CollectionIntervalSeconds": int64(s.currentCollectionInterval() / time.Second),
		"Range":                     "5h",
		"TrendData":                 nil,
	}

	if s.title != "" {
		data["Title"] = "WebUsage - " + s.title
	} else {
		data["Title"] = "WebUsage"
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "layout", data); err != nil {
		s.logger.Error("Template execution failed", "error", err)
	}
}

// buildSSRProviderView returns the projection-rich provider summary used by
// handleDashboard and direct SSR assertions. It deliberately keeps all
// transport-sensitive fields out of the view model.
func (s *Server) buildSSRProviderView(legacy domain.ProviderView, now time.Time) SSRProviderView {
	ssr := SSRProviderView{ProviderView: legacy, DisplayCycleType: legacy.CycleType, MetricProjections: map[string]map[string]interface{}{}}
	provider, err := s.store.GetProvider(legacy.ID)
	if err != nil {
		return ssr
	}
	sets, err := loadMetricSnapshotSets(s.store, provider, now)
	if err != nil {
		return ssr
	}
	if primary := primaryMetricSet(provider, sets); primary != nil {
		ssr.PrimaryMetric = primary.Metric
	}
	for _, set := range sets {
		limit := 0.0
		if set.Latest != nil && set.Latest.Limit != nil {
			limit = *set.Latest.Limit
		}
		resetAt := time.Time{}
		if set.Latest != nil && set.Latest.ResetAt != nil {
			resetAt = *set.Latest.ResetAt
		}
		projection := set.Projection
		ssr.Metrics = append(ssr.Metrics, SSRMetricView{
			MetricView: domain.MetricView{
				Name: set.Metric, Label: domain.MetricLabel(set.Metric), Used: projection.CurrentUsage,
				Limit: limit, Percent: projection.CurrentPercent, ResetAt: resetAt,
			},
			Metric: set.Metric, CycleType: string(projection.CycleType), CycleLabel: projection.CycleLabel,
			CurrentPercent: projection.CurrentPercent, PacePerHour: projection.PacePerHour,
			ProjectedUsage: projection.ProjectedUsage, ProjectedPercent: projection.ProjectedPercent,
			HasProjection: projection.HasProjection, HasForecast: projection.HasForecast,
			ForecastUsable: projection.ForecastUsable, WeakEstimate: projection.WeakEstimate,
			Severity: projection.Severity, TimeRemaining: projection.TimeRemainingText,
			ObservationWindowHours: projection.ObservationWindowHours,
		})
		ssr.MetricProjections[set.Metric] = metricProjectionJSON(set)
	}
	if len(ssr.Metrics) > 0 {
		ssr.DisplayCycleType = ssr.Metrics[0].CycleType
	}
	return ssr
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

func (s *Server) beginCollectionRun() *collectionRun {
	s.collectionMu.Lock()
	defer s.collectionMu.Unlock()
	s.nextCollectionID++
	id := fmt.Sprintf("collection-%d", s.nextCollectionID)
	run := &collectionRun{
		ID:        id,
		Status:    "collecting",
		StartedAt: time.Now().UTC(),
	}
	if s.collectionRuns == nil {
		s.collectionRuns = make(map[string]*collectionRun)
	}
	s.collectionRuns[id] = run
	s.latestCollectionID = id
	return run
}

func (s *Server) finishCollectionRun(id string, err error) {
	s.collectionMu.Lock()
	defer s.collectionMu.Unlock()
	run, ok := s.collectionRuns[id]
	if !ok {
		return
	}
	run.Terminal = true
	run.CompletedAt = time.Now().UTC()
	if err != nil {
		run.Status = "failed"
		message := "collection failed"
		run.CollectionError = &message
		return
	}
	run.Status = "completed"
}

func (s *Server) collectionRunSnapshot(id string) (collectionRun, bool) {
	s.collectionMu.RLock()
	defer s.collectionMu.RUnlock()
	if id == "" {
		id = s.latestCollectionID
	}
	run, ok := s.collectionRuns[id]
	if !ok || run == nil {
		return collectionRun{}, false
	}
	snapshot := *run
	if run.CollectionError != nil {
		message := *run.CollectionError
		snapshot.CollectionError = &message
	}
	return snapshot, true
}

func collectionRunResponse(run collectionRun) map[string]interface{} {
	response := map[string]interface{}{
		"collection_id": run.ID,
		"status":        run.Status,
		"terminal":      run.Terminal,
		"done":          run.Terminal,
		"success":       run.Terminal && run.Status == "completed",
		"started_at":    run.StartedAt,
	}
	if !run.CompletedAt.IsZero() {
		response["completed_at"] = run.CompletedAt
	}
	if run.CollectionError != nil {
		response["collection_error"] = *run.CollectionError
		// Preserve the generic legacy error key for clients that already render it.
		response["error"] = *run.CollectionError
	}
	return response
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

	run := s.beginCollectionRun()
	go func(collectionID string) {
		err := s.collector.CollectAll(context.Background())
		if err != nil {
			s.logger.Error("Manual collection failed", "error", err)
		}
		s.finishCollectionRun(collectionID, err)
	}(run.ID)

	s.jsonResponse(w, map[string]interface{}{
		"status":        "collecting",
		"message":       "Collection triggered from OpenUsage API",
		"collection_id": run.ID,
		"status_url":    "/api/collect/status?collection_id=" + run.ID,
	})
}

// handleCollectionStatus reports the terminal state of an asynchronous manual collection.
// The query form is canonical; /api/collect/status/{id} is retained as a path alias.
func (s *Server) handleCollectionStatus(w nethttp.ResponseWriter, r *nethttp.Request) {
	if r.Method != nethttp.MethodGet {
		nethttp.Error(w, "Method not allowed", nethttp.StatusMethodNotAllowed)
		return
	}
	id := r.URL.Query().Get("collection_id")
	if id == "" {
		id = strings.TrimPrefix(r.URL.Path, "/api/collect/status/")
		if id == r.URL.Path {
			id = ""
		}
	}
	run, ok := s.collectionRunSnapshot(id)
	if !ok {
		s.jsonError(w, "Collection not found", nethttp.StatusNotFound)
		return
	}
	s.jsonResponse(w, collectionRunResponse(run))
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
	if err := json.NewEncoder(w).Encode(data); err != nil {
		s.logger.Error("Failed to encode JSON response", "error", err)
	}
}

// jsonError sends a JSON error response
func (s *Server) jsonError(w nethttp.ResponseWriter, message string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(map[string]string{"error": message}); err != nil {
		s.logger.Error("Failed to encode JSON error response", "error", err)
	}
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
	s.mux.HandleFunc("/api/metric-preferences", s.handleMetricPreferences)
	s.mux.HandleFunc("/api/heatmap", s.handleAPIHeatmap)
	s.mux.HandleFunc("/api/activity", s.handleAPIActivity)
	s.mux.HandleFunc("/api/collect", s.handleCollect)
	s.mux.HandleFunc("/api/collect/status", s.handleCollectionStatus)
	s.mux.HandleFunc("/api/collect/status/", s.handleCollectionStatus)
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
		if err := server.Shutdown(shutdownCtx); err != nil {
			s.logger.Error("Failed to shut down HTTP server", "error", err)
		}
	}()

	s.logger.Info("Starting HTTP server", "address", addr)
	if err := server.ListenAndServe(); err != nethttp.ErrServerClosed {
		return err
	}
	return nil
}
