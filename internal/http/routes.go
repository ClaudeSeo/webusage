package http

// setupRoutes configures HTTP routes
func (s *Server) setupRoutes() {
	// API endpoints
	s.mux.HandleFunc("/api/current", s.handleCurrentUsage)
	s.mux.HandleFunc("/api/trends", s.handleTrends)
	s.mux.HandleFunc("/api/providers", s.handleProviders)
	// /api/providers/{name}/enable, /api/providers/{name}/disable
	s.mux.HandleFunc("/api/providers/", s.handleProviderAction)
	s.mux.HandleFunc("/healthz", s.handleHealthz)

	// Web UI
	s.mux.HandleFunc("/", s.handleDashboard)
}

