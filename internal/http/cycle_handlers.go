package http

import (
	"fmt"
	nethttp "net/http"
	"strconv"
	"time"

	"github.com/ClaudeSeo/webusage/internal/domain"
	"github.com/ClaudeSeo/webusage/internal/provider"
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
		hour := dp.Timestamp.Truncate(time.Hour)
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
	result := make(map[string]*domain.CurrentCycleInfo)

	for _, p := range providers {
		cycleConfig := domain.GetProviderCycleConfig(p.Name)

		info := &domain.CurrentCycleInfo{
			ProviderID:   p.Name,
			DisplayName:  getDisplayName(p.Name, s.registry),
			Enabled:      p.Enabled,
			CycleType:    string(cycleConfig.CycleType),
			LimitType:    string(cycleConfig.LimitType),
			LimitValue:   cycleConfig.LimitValue,
			LastUpdated:  p.LastRun,
			Error:        p.LastError,
		}

		// Get latest usage data
		snapshots, err := s.store.GetLatestUsageByProvider(p.ID)
		if err != nil || len(snapshots) == 0 {
			result[p.Name] = info
			continue
		}

		// Find the primary metric (session for rolling_5h, credits for monthly)
		var primarySnapshot *store.UsageSnapshot
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
		// Fallback to first snapshot if no primary metric found
		if primarySnapshot == nil && len(snapshots) > 0 {
			primarySnapshot = snapshots[0]
		}

		if primarySnapshot != nil {
			info.CurrentUsage = primarySnapshot.Used
			if primarySnapshot.Limit != nil && *primarySnapshot.Limit > 0 {
				info.UsagePercent = (primarySnapshot.Used / *primarySnapshot.Limit) * 100
				info.LimitValue = primarySnapshot.Limit
			}

			// Calculate cycle boundaries
			info.CycleStart, info.CycleEnd = domain.CalculateCycleBoundaries(
				cycleConfig.CycleType,
				now,
				primarySnapshot.ResetAt,
			)

			// Calculate time remaining
			if info.CycleEnd != nil {
				info.TimeRemaining = domain.FormatDuration(info.CycleEnd.Sub(now))
			}

			// Get trend data for pace calculation
			if info.CycleStart != nil {
				startTime := *info.CycleStart
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
					info.CurrentPace, info.BaselinePace, info.PaceVsBaselineRatio = domain.CalculatePace(points)
				}
			}

			// Forecast limit exceedance
			if info.CurrentPace > 0 && info.CycleEnd != nil {
				info.ForecastLimitAt, info.WillExceedBeforeReset = domain.ForecastLimitExceedTime(
					info.CurrentUsage,
					info.LimitValue,
					info.CurrentPace,
					info.CycleEnd,
				)
			}
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

	// Parse query parameters
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

	cycleConfig := domain.GetProviderCycleConfig(p.Name)
	now := time.Now()

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

	// Calculate time range based on view
	var startTime, endTime time.Time
	cycleStart, cycleEnd := domain.CalculateCycleBoundaries(cycleConfig.CycleType, now, nil)

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
		ProviderID: p.Name,
		CycleType:  string(cycleConfig.CycleType),
		View:       view,
		Mode:       mode,
		Bucket:     bucketSize,
		Data:       points,
		CycleStart: cycleStart,
		CycleEnd:   cycleEnd,
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
	var result []domain.ForecastInfo

	for _, p := range providers {
		// Filter by provider_id if specified
		if providerID != "" && p.Name != providerID {
			continue
		}

		cycleConfig := domain.GetProviderCycleConfig(p.Name)

		info := domain.ForecastInfo{
			ProviderID:   p.Name,
			DisplayName:  getDisplayName(p.Name, s.registry),
			CycleType:    string(cycleConfig.CycleType),
		}

		// Get latest usage data
		snapshots, err := s.store.GetLatestUsageByProvider(p.ID)
		if err != nil || len(snapshots) == 0 {
			result = append(result, info)
			continue
		}

		// Find primary metric
		var primarySnapshot *store.UsageSnapshot
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
		if primarySnapshot == nil && len(snapshots) > 0 {
			primarySnapshot = snapshots[0]
		}

		if primarySnapshot != nil {
			info.CurrentUsage = primarySnapshot.Used
			if primarySnapshot.Limit != nil {
				info.LimitValue = primarySnapshot.Limit
				info.Remaining = *primarySnapshot.Limit - primarySnapshot.Used
			}

			// Calculate cycle boundaries
			_, cycleEnd := domain.CalculateCycleBoundaries(cycleConfig.CycleType, now, primarySnapshot.ResetAt)
			info.CycleEnd = cycleEnd

			// Get trend data for forecasting
			if cycleEnd != nil {
				startTime := cycleEnd.Add(-30 * 24 * time.Hour)
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
					currentPace, _, _ := domain.CalculatePace(points)

					// Calculate forecast
					info.ForecastLimitAt, info.WillExceedBeforeReset = domain.ForecastLimitExceedTime(
						info.CurrentUsage,
						info.LimitValue,
						currentPace,
						info.CycleEnd,
					)

					// Calculate hours until limit
					if info.ForecastLimitAt != nil {
						hours := info.ForecastLimitAt.Sub(now).Hours()
						if hours > 0 {
							info.HoursUntilLimit = &hours
						}
					}

					// Confidence calculation (based on data points count)
					if len(points) >= 10 {
						info.Confidence = 0.9
					} else if len(points) >= 5 {
						info.Confidence = 0.7
					} else {
						info.Confidence = 0.5
					}
				}
			}

			// Determine recommended action
			if info.WillExceedBeforeReset {
				info.RecommendedAction = "reduce_usage"
			} else if info.LimitValue != nil && info.Remaining < (*info.LimitValue * 0.2) {
				info.RecommendedAction = "monitor_closely"
			} else {
				info.RecommendedAction = "normal"
			}
		}

		result = append(result, info)
	}

	// Return single object if provider_id specified, else array
	if providerID != "" && len(result) == 1 {
		s.jsonResponse(w, result[0])
	} else {
		s.jsonResponse(w, map[string]interface{}{
			"forecasts": result,
		})
	}
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

	var result []domain.ProviderMetadata

	for _, p := range providers {
		cycleConfig := domain.GetProviderCycleConfig(p.Name)

		meta := domain.ProviderMetadata{
			ProviderID:      p.Name,
			DisplayName:     getDisplayName(p.Name, s.registry),
			AuthMethod:      "unknown",
			Enabled:         p.Enabled,
			CycleType:       string(cycleConfig.CycleType),
			LimitType:       string(cycleConfig.LimitType),
			LimitValue:      cycleConfig.LimitValue,
			SupportedViews:  []string{"current", "previous", "both"},
			SupportedModes:  []string{"absolute", "relative", "rate"},
			SupportedBuckets: []string{"auto", "hour", "day", "cycle"},
		}

		// Get auth method from registry
		if s.registry != nil {
			if rp, ok := s.registry.Get(p.Name); ok {
				meta.AuthMethod = string(rp.AuthMethod())
			}
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
func getDisplayName(name string, registry *provider.Registry) string {
	if registry != nil {
		if p, ok := registry.Get(name); ok {
			return p.DisplayName()
		}
	}

	// Fallback display names
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