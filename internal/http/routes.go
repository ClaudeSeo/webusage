package http

// setupRoutes configures HTTP routes
func (s *Server) setupRoutes() {
	// API endpoints - Legacy (maintain backward compatibility)
	s.mux.HandleFunc("/api/current", s.handleCurrentUsage)
	s.mux.HandleFunc("/api/trends", s.handleTrends)
	s.mux.HandleFunc("/api/providers", s.handleProvidersLegacy)
	// /api/providers/{name}/enable, /api/providers/{name}/disable
	s.mux.HandleFunc("/api/providers/", s.handleProviderAction)
	s.mux.HandleFunc("/api/collect", s.handleCollect)
	s.mux.HandleFunc("/healthz", s.handleHealthz)

	// API endpoints - Cycle-Aware (new endpoints)
	s.mux.HandleFunc("/api/v1/current", s.handleAPICurrent)
	s.mux.HandleFunc("/api/v1/trends", s.handleAPITrends)
	s.mux.HandleFunc("/api/v1/forecast", s.handleAPIForecast)
	s.mux.HandleFunc("/api/v1/providers", s.handleAPIProvidersMeta)

	// Web UI
	s.mux.HandleFunc("/", s.handleDashboard)
}

