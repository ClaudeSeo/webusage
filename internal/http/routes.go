package http

// setupRoutes configures HTTP routes
func (s *Server) setupRoutes() {
	// API endpoints
	s.mux.HandleFunc("/api/current", s.handleAPICurrent)
	s.mux.HandleFunc("/api/trends", s.handleAPITrends)
	s.mux.HandleFunc("/api/forecast", s.handleAPIForecast)
	s.mux.HandleFunc("/api/providers", s.handleAPIProvidersMeta)
	s.mux.HandleFunc("/api/providers/", s.handleProviderAction)
	s.mux.HandleFunc("/api/collect", s.handleCollect)
	s.mux.HandleFunc("/healthz", s.handleHealthz)

	// Web UI
	s.mux.HandleFunc("/", s.handleDashboard)
}

