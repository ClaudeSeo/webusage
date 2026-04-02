package http

import (
	"fmt"
	nethttp "net/http"
	"strconv"
	"time"

	"github.com/ClaudeSeo/webusage/internal/domain"
	"github.com/ClaudeSeo/webusage/internal/store"
)

// ============================================================================
// Helper Functions (using domain package)
// ============================================================================

// formatDuration wraps domain.FormatDuration for local use
func formatDuration(d time.Duration) string {
	return domain.FormatDuration(d)
}

// calculateCycleBoundaries wraps domain.CalculateCycleBoundaries for local use
func calculateCycleBoundaries(cycleType domain.CycleType, now time.Time, resetAt *time.Time) (*time.Time, *time.Time) {
	return domain.CalculateCycleBoundaries(cycleType, now, resetAt)
}

// calculatePace wraps domain.CalculatePace for local use
func calculatePace(data []domain.TrendDataPoint) (currentPace, baselinePace, ratio float64) {
	return domain.CalculatePace(data)
}

// forecastLimitExceedTime wraps domain.ForecastLimitExceedTime for local use
func forecastLimitExceedTime(currentUsage float64, limitValue *float64, pace float64, cycleEnd *time.Time) (*time.Time, bool) {
	return domain.ForecastLimitExceedTime(currentUsage, limitValue, pace, cycleEnd)
}

// getProviderCycleConfig wraps domain.GetProviderCycleConfig for local use
func getProviderCycleConfig(providerName string) domain.ProviderCycleConfig {
	return domain.GetProviderCycleConfig(providerName)
}

// getBucketSizeForCycle determines the appropriate bucket size based on cycle type
func getBucketSizeForCycle(cycleType domain.CycleType, requestedBucket string) string {
	if requestedBucket != "auto" && requestedBucket != "" {
		return requestedBucket
	}

	switch cycleType {
	case domain.CycleTypeRolling5h:
		return "hour"
	case domain.CycleTypeDaily:
		return "hour"
	case domain.CycleTypeWeekly:
		return "day"
	case domain.CycleTypeMonthly:
		return "day"
	default:
		return "hour"
	}
}

// aggregateDataByBucket aggregates trend data by bucket size
func aggregateDataByBucket(data []domain.TrendDataPoint, bucket string) []domain.TrendDataPoint {
	if len(data) == 0 {
		return data
	}

	// Sort by timestamp
	sorted := make([]domain.TrendDataPoint, len(data))
	copy(sorted, data)
	for i := 0; i < len(sorted)-1; i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[i].Timestamp.After(sorted[j].Timestamp) {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	if bucket == "hour" {
		return aggregateByHour(sorted)
	} else if bucket == "day" {
		return aggregateByDay(sorted)
	}

	return sorted
}

func aggregateByHour(data []domain.TrendDataPoint) []domain.TrendDataPoint {
	bucketMap := make(map[time.Time]float64)

	for _, dp := range data {
		hour := time.Date(dp.Timestamp.Year(), dp.Timestamp.Month(), dp.Timestamp.Day(), dp.Timestamp.Hour(), 0, 0, 0, dp.Timestamp.Location())
		bucketMap[hour] = dp.Value // Take latest value for the hour
	}

	var result []domain.TrendDataPoint
	for t, v := range bucketMap {
		result = append(result, domain.TrendDataPoint{
			Timestamp: t,
			Value:     v,
			Metric:    data[0].Metric,
		})
	}

	// Sort
	for i := 0; i < len(result)-1; i++ {
		for j := i + 1; j < len(result); j++ {
			if result[i].Timestamp.After(result[j].Timestamp) {
				result[i], result[j] = result[j], result[i]
			}
		}
	}

	return result
}

func aggregateByDay(data []domain.TrendDataPoint) []domain.TrendDataPoint {
	bucketMap := make(map[time.Time]float64)

	for _, dp := range data {
		day := time.Date(dp.Timestamp.Year(), dp.Timestamp.Month(), dp.Timestamp.Day(), 0, 0, 0, 0, dp.Timestamp.Location())
		bucketMap[day] = dp.Value // Take latest value for the day
	}

	var result []domain.TrendDataPoint
	for t, v := range bucketMap {
		result = append(result, domain.TrendDataPoint{
			Timestamp: t,
			Value:     v,
			Metric:    data[0].Metric,
		})
	}

	// Sort
	for i := 0; i < len(result)-1; i++ {
		for j := i + 1; j < len(result); j++ {
			if result[i].Timestamp.After(result[j].Timestamp) {
				result[i], result[j] = result[j], result[i]
			}
		}
	}

	return result
}

// ============================================================================
// API Handlers
// ============================================================================

// handleAPICurrent returns current cycle-aware usage for all providers
// GET /api/current
func (s *Server) handleAPICurrent(w nethttp.ResponseWriter, r *nethttp.Request) {
	if r.Method != nethttp.MethodGet {
		nethttp.Error(w, "Method not allowed", nethttp.StatusMethodNotAllowed)
		return
	}

	providers, err := s.store.ListProviders()
	if err != nil {
		s.jsonError(w, "Failed to list providers", nethttp.StatusInternalServerError)
		return
	}

	now := time.Now()
	result := make(map[string]interface{})

	for _, p := range providers {
		if !p.Enabled {
			continue
		}

		cycleConfig := getProviderCycleConfig(p.Name)

		// Get latest snapshots for this provider
		snapshots, err := s.store.GetLatestUsageByProvider(p.ID)
		if err != nil {
			s.logger.Warn("Failed to get latest snapshots", "provider", p.Name, "error", err)
			continue
		}

		// Determine primary metric based on cycle type
		var primaryMetric string
		switch cycleConfig.CycleType {
		case domain.CycleTypeRolling5h:
			primaryMetric = "session"
		case domain.CycleTypeMonthly:
			primaryMetric = "premium_interactions"
		default:
			primaryMetric = ""
		}

		// Find primary metric snapshot
		var primarySnapshot *store.UsageSnapshot
		for _, snap := range snapshots {
			if snap.Metric == primaryMetric {
				primarySnapshot = snap
				break
			}
		}
		// Fallback to first snapshot if no primary metric found
		if primarySnapshot == nil && len(snapshots) > 0 {
			primarySnapshot = snapshots[0]
		}

		info := map[string]interface{}{
			"provider_id":              p.Name,
			"display_name":             getDisplayName(p.Name),
			"enabled":                  p.Enabled,
			"cycle_type":               string(cycleConfig.CycleType),
			"limit_type":               string(cycleConfig.LimitType),
			"current_usage":            0.0,
			"usage_percent":            0.0,
			"will_exceed_before_reset": false,
			"current_pace":             0.0,
			"baseline_pace":             0.0,
			"pace_vs_baseline_ratio":   0.0,
		}

		if primarySnapshot != nil {
			info["current_usage"] = primarySnapshot.Used
			if primarySnapshot.Limit != nil && *primarySnapshot.Limit > 0 {
				info["usage_percent"] = (primarySnapshot.Used / *primarySnapshot.Limit) * 100
				info["limit_value"] = *primarySnapshot.Limit
			}

			// Calculate cycle boundaries
			info["cycle_start"], info["cycle_end"] = calculateCycleBoundaries(
				cycleConfig.CycleType,
				now,
				primarySnapshot.ResetAt,
			)

			// Calculate time remaining
			if cycleEnd, ok := info["cycle_end"].(*time.Time); ok && cycleEnd != nil {
				info["time_remaining"] = formatDuration(cycleEnd.Sub(now))
			}

			// Get trend data for pace calculation
			if cycleStart, ok := info["cycle_start"].(*time.Time); ok && cycleStart != nil {
				startTime := *cycleStart
				if startTime.Before(now.Add(-30 * 24 * time.Hour)) {
					startTime = now.Add(-30 * 24 * time.Hour)
				}
				trendData, _ := s.store.GetUsageTrends(p.ID, primarySnapshot.Metric, startTime, now)
				if len(trendData) > 0 {
					points := make([]domain.TrendDataPoint, len(trendData))
					for i, td := range trendData {
						points[i] = domain.TrendDataPoint{
							Timestamp: td.CollectedAt,
							Value:     td.Used,
							Metric:    td.Metric,
						}
					}
					info["current_pace"], info["baseline_pace"], info["pace_vs_baseline_ratio"] = calculatePace(points)
				}
			}

			// Forecast limit exceedance
			currentPace, _ := info["current_pace"].(float64)
			limitValue, _ := info["limit_value"].(*float64)
			cycleEnd, _ := info["cycle_end"].(*time.Time)
			if currentPace > 0 && limitValue != nil && cycleEnd != nil {
				info["forecast_limit_at"], info["will_exceed_before_reset"] = forecastLimitExceedTime(
					primarySnapshot.Used,
					limitValue,
					currentPace,
					cycleEnd,
				)
			}

			info["last_updated"] = primarySnapshot.CollectedAt.Format(time.RFC3339)
		}

		result[p.Name] = info
	}

	s.jsonResponse(w, result)
}

// handleAPITrends returns cycle-aware trend data
// GET /api/trends?provider_id=&view=&mode=&bucket=
func (s *Server) handleAPITrends(w nethttp.ResponseWriter, r *nethttp.Request) {
	if r.Method != nethttp.MethodGet {
		nethttp.Error(w, "Method not allowed", nethttp.StatusMethodNotAllowed)
		return
	}

	providerID := r.URL.Query().Get("provider_id")
	view := r.URL.Query().Get("view")
	if view == "" {
		view = "current"
	}
	mode := r.URL.Query().Get("mode")
	if mode == "" {
		mode = "absolute"
	}
	bucket := r.URL.Query().Get("bucket")
	if bucket == "" {
		bucket = "auto"
	}

	if providerID == "" {
		s.jsonError(w, "provider_id is required", nethttp.StatusBadRequest)
		return
	}

	// Get provider from store
	p, err := s.store.GetProviderByName(providerID)
	if err != nil {
		s.jsonError(w, fmt.Sprintf("Provider '%s' not found", providerID), nethttp.StatusNotFound)
		return
	}

	cycleConfig := getProviderCycleConfig(p.Name)
	now := time.Now()

	// Calculate time range based on view
	var startTime, endTime time.Time
	cycleStart, cycleEnd := calculateCycleBoundaries(cycleConfig.CycleType, now, nil)

	switch view {
	case "current":
		if cycleStart != nil {
			startTime = *cycleStart
		} else {
			startTime = now.Add(-24 * time.Hour)
		}
		endTime = now
	case "previous":
		if cycleStart != nil && cycleEnd != nil {
			duration := cycleEnd.Sub(*cycleStart)
			endTime = *cycleStart
			startTime = endTime.Add(-duration)
		} else {
			endTime = now.Add(-24 * time.Hour)
			startTime = endTime.Add(-24 * time.Hour)
		}
	case "both":
		if cycleStart != nil {
			duration := cycleEnd.Sub(*cycleStart)
			startTime = cycleStart.Add(-duration)
		} else {
			startTime = now.Add(-48 * time.Hour)
		}
		endTime = now
	default:
		startTime = now.Add(-24 * time.Hour)
		endTime = now
	}

	// Determine primary metric based on cycle type
	var primaryMetric string
	switch cycleConfig.CycleType {
	case domain.CycleTypeRolling5h:
		primaryMetric = "session"
	case domain.CycleTypeMonthly:
		primaryMetric = "premium_interactions"
	default:
		primaryMetric = ""
	}

	// Get trend data
	snapshots, err := s.store.GetUsageTrends(p.ID, primaryMetric, startTime, endTime)
	if err != nil {
		s.jsonError(w, "Failed to get trend data", nethttp.StatusInternalServerError)
		return
	}

	// Convert to trend points
	var points []domain.TrendDataPoint
	for _, snap := range snapshots {
		value := snap.Used

		// Apply mode transformation
		switch mode {
		case "relative":
			// Relative to cycle start
			if len(snapshots) > 0 && snapshots[0].Metric == snap.Metric {
				value = snap.Used - snapshots[0].Used
			}
		case "rate":
			// Rate of change (not implemented in this version)
		}

		points = append(points, domain.TrendDataPoint{
			Timestamp: snap.CollectedAt,
			Value:     value,
			Metric:    snap.Metric,
		})
	}

	// Apply bucket aggregation
	bucketSize := getBucketSizeForCycle(cycleConfig.CycleType, bucket)
	points = aggregateDataByBucket(points, bucketSize)

	result := domain.ProviderTrends{
		ProviderID: providerID,
		CycleType:  string(cycleConfig.CycleType),
		View:       view,
		Mode:       mode,
		Bucket:     bucketSize,
		Data:       points,
	}

	if cycleStart != nil {
		result.CycleStart = cycleStart
	}
	if cycleEnd != nil {
		result.CycleEnd = cycleEnd
	}

	s.jsonResponse(w, result)
}

// handleAPIForecast returns usage forecast for all providers or specific provider
// GET /api/forecast?provider_id=
func (s *Server) handleAPIForecast(w nethttp.ResponseWriter, r *nethttp.Request) {
	if r.Method != nethttp.MethodGet {
		nethttp.Error(w, "Method not allowed", nethttp.StatusMethodNotAllowed)
		return
	}

	providerID := r.URL.Query().Get("provider_id")

	providers, err := s.store.ListProviders()
	if err != nil {
		s.jsonError(w, "Failed to list providers", nethttp.StatusInternalServerError)
		return
	}

	now := time.Now()
	result := make(map[string]interface{})

	for _, p := range providers {
		if !p.Enabled {
			continue
		}

		// Filter by provider_id if specified
		if providerID != "" && p.Name != providerID {
			continue
		}

		cycleConfig := getProviderCycleConfig(p.Name)
		snapshots, err := s.store.GetLatestUsageByProvider(p.ID)
		if err != nil {
			continue
		}

		// Determine primary metric based on cycle type
		var primaryMetric string
		switch cycleConfig.CycleType {
		case domain.CycleTypeRolling5h:
			primaryMetric = "session"
		case domain.CycleTypeMonthly:
			primaryMetric = "premium_interactions"
		default:
			primaryMetric = ""
		}

		// Find primary snapshot
		var primarySnapshot *store.UsageSnapshot
		for _, snap := range snapshots {
			if snap.Metric == primaryMetric {
				primarySnapshot = snap
				break
			}
		}
		if primarySnapshot == nil && len(snapshots) > 0 {
			primarySnapshot = snapshots[0]
		}

		if primarySnapshot == nil {
			continue
		}

		// Calculate forecast
		cycleStart, cycleEnd := calculateCycleBoundaries(cycleConfig.CycleType, now, primarySnapshot.ResetAt)
		if cycleEnd == nil {
			continue
		}

		forecast := map[string]interface{}{
			"provider_id":   p.Name,
			"cycle_type":    string(cycleConfig.CycleType),
			"current_usage": primarySnapshot.Used,
			"cycle_end":     cycleEnd,
			"time_remaining": formatDuration(cycleEnd.Sub(now)),
			"confidence":    0.8, // Default confidence
		}

		if primarySnapshot.Limit != nil && *primarySnapshot.Limit > 0 {
			forecast["limit"] = *primarySnapshot.Limit

			// Calculate pace from trend data
			if cycleStart != nil {
				trendData, _ := s.store.GetUsageTrends(p.ID, primarySnapshot.Metric, *cycleStart, now)
				if len(trendData) > 1 {
					points := make([]domain.TrendDataPoint, len(trendData))
					for i, td := range trendData {
						points[i] = domain.TrendDataPoint{
							Timestamp: td.CollectedAt,
							Value:     td.Used,
							Metric:    td.Metric,
						}
					}
					currentPace, baselinePace, _ := calculatePace(points)
					forecast["current_pace"] = currentPace
					forecast["baseline_pace"] = baselinePace

					// Forecast exceed time
					forecastAt, willExceed := forecastLimitExceedTime(
						primarySnapshot.Used,
						primarySnapshot.Limit,
						currentPace,
						cycleEnd,
					)
					forecast["will_exceed_before_reset"] = willExceed
					if forecastAt != nil {
						forecast["forecast_limit_at"] = forecastAt
					}
				}
			}
		}

		result[p.Name] = forecast
	}

	// Wrap forecasts in "forecasts" key for API contract compatibility
	var forecasts []map[string]interface{}
	for _, f := range result {
		if m, ok := f.(map[string]interface{}); ok {
			forecasts = append(forecasts, m)
		}
	}

	// Return single object if provider_id specified, else array
	if providerID != "" && len(forecasts) == 1 {
		s.jsonResponse(w, forecasts[0])
		return
	}

	s.jsonResponse(w, map[string]interface{}{
		"forecasts": forecasts,
	})
}

// handleAPIProvidersMeta returns provider metadata with cycle information
// GET /api/providers
func (s *Server) handleAPIProvidersMeta(w nethttp.ResponseWriter, r *nethttp.Request) {
	if r.Method != nethttp.MethodGet {
		nethttp.Error(w, "Method not allowed", nethttp.StatusMethodNotAllowed)
		return
	}

	providers, err := s.store.ListProviders()
	if err != nil {
		s.jsonError(w, "Failed to list providers", nethttp.StatusInternalServerError)
		return
	}

	type ProviderMeta struct {
		ProviderID      string   `json:"provider_id"`
		DisplayName     string   `json:"display_name"`
		AuthMethod      string   `json:"auth_method"`
		Enabled         bool     `json:"enabled"`
		CycleType       string   `json:"cycle_type"`
		LimitType       string   `json:"limit_type"`
		Metrics         []string `json:"metrics"`
		SupportedViews  []string `json:"supported_views"`
		SupportedModes  []string `json:"supported_modes"`
		SupportedBuckets []string `json:"supported_buckets"`
	}

	var result []ProviderMeta

	for _, p := range providers {
		cycleConfig := getProviderCycleConfig(p.Name)

		meta := ProviderMeta{
			ProviderID:  p.Name,
			DisplayName: getDisplayName(p.Name),
			Enabled:     p.Enabled,
			CycleType:   string(cycleConfig.CycleType),
			LimitType:   string(cycleConfig.LimitType),
			SupportedViews: []string{"current", "previous", "both"},
			SupportedModes: []string{"absolute", "relative", "rate"},
			SupportedBuckets: []string{"auto", "hour", "day", "cycle"},
		}

		// Get available metrics
		snapshots, _ := s.store.GetLatestUsageByProvider(p.ID)
		for _, snap := range snapshots {
			meta.Metrics = append(meta.Metrics, snap.Metric)
		}

		result = append(result, meta)
	}

	s.jsonResponse(w, map[string]interface{}{
		"providers": result,
	})
}

// getDisplayName returns the display name for a provider
func getDisplayName(name string) string {
	displayNames := map[string]string{
		"claude":  "Claude",
		"codex":   "Codex",
		"copilot": "GitHub Copilot",
	}
	if dn, ok := displayNames[name]; ok {
		return dn
	}
	return name
}

// parseBool parses a boolean query parameter
func parseBool(s string) bool {
	b, _ := strconv.ParseBool(s)
	return b
}

// parseFloat parses a float query parameter
func parseFloat(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

// parseInt parses an int query parameter
func parseInt(s string) int {
	i, _ := strconv.Atoi(s)
	return i
}