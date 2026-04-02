package http

import (
	"fmt"
	nethttp "net/http"
	"strconv"
	"time"

	"github.com/ClaudeSeo/webusage/internal/provider"
	"github.com/ClaudeSeo/webusage/internal/store"
)

// ============================================================================
// Cycle Types and Configuration
// ============================================================================

// CycleType represents the reset cycle type for a provider
type CycleType string

const (
	CycleTypeRolling5h CycleType = "rolling_5h" // 5-hour rolling window (Codex session)
	CycleTypeDaily     CycleType = "daily"      // Daily reset
	CycleTypeWeekly    CycleType = "weekly"     // Weekly reset (7 days)
	CycleTypeMonthly   CycleType = "monthly"    // Monthly reset (billing cycle)
)

// LimitType represents the limit constraint type
type LimitType string

const (
	LimitTypeLimited   LimitType = "limited"
	LimitTypeUnlimited LimitType = "unlimited"
	LimitTypeUnknown   LimitType = "unknown"
)

// ProviderConfig holds cycle-aware configuration for each provider
// This maps provider_id to its cycle configuration
type ProviderCycleConfig struct {
	CycleType  CycleType `json:"cycle_type"`
	LimitType  LimitType `json:"limit_type"`
	LimitValue *float64  `json:"limit_value,omitempty"`
}

// providerCycleConfigs defines cycle configurations for known providers
var providerCycleConfigs = map[string]ProviderCycleConfig{
	"claude": {
		CycleType: CycleTypeRolling5h,
		LimitType: LimitTypeLimited,
	},
	"codex": {
		CycleType: CycleTypeRolling5h,
		LimitType: LimitTypeLimited,
	},
	"copilot": {
		CycleType: CycleTypeMonthly,
		LimitType: LimitTypeLimited,
	},
}

// getProviderCycleConfig returns cycle config for a provider
func getProviderCycleConfig(providerName string) ProviderCycleConfig {
	if cfg, ok := providerCycleConfigs[providerName]; ok {
		return cfg
	}
	// Default to daily cycle with unknown limit
	return ProviderCycleConfig{
		CycleType: CycleTypeDaily,
		LimitType: LimitTypeUnknown,
	}
}

// ============================================================================
// Data Models
// ============================================================================

// CurrentCycleInfo represents current cycle state for a provider
type CurrentCycleInfo struct {
	ProviderID         string     `json:"provider_id"`
	DisplayName        string     `json:"display_name"`
	Enabled            bool       `json:"enabled"`
	CycleType          string     `json:"cycle_type"`
	LimitType          string     `json:"limit_type"`
	LimitValue         *float64   `json:"limit_value,omitempty"`
	CurrentUsage       float64    `json:"current_usage"`
	UsagePercent       float64    `json:"usage_percent"`
	CycleStart         *time.Time `json:"cycle_start,omitempty"`
	CycleEnd           *time.Time `json:"cycle_end,omitempty"`
	TimeRemaining      string     `json:"time_remaining,omitempty"`
	ForecastLimitAt    *time.Time `json:"forecast_limit_at,omitempty"`
	WillExceedBeforeReset bool    `json:"will_exceed_before_reset"`
	CurrentPace        float64    `json:"current_pace"`          // usage per hour
	BaselinePace       float64    `json:"baseline_pace"`         // expected normal pace
	PaceVsBaselineRatio float64   `json:"pace_vs_baseline_ratio"` // current_pace / baseline_pace
	LastUpdated        *time.Time `json:"last_updated,omitempty"`
	Error              *string    `json:"error,omitempty"`
}

// TrendDataPoint represents a single data point in trend series
type TrendDataPoint struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
	Metric    string    `json:"metric"`
}

// ProviderTrends represents trend data for a provider
type ProviderTrends struct {
	ProviderID  string           `json:"provider_id"`
	CycleType   string           `json:"cycle_type"`
	View        string           `json:"view"`        // current, previous, both
	Mode        string           `json:"mode"`        // absolute, relative, rate
	Bucket      string           `json:"bucket"`      // auto, hour, day, cycle
	Data        []TrendDataPoint `json:"data"`
	CycleStart  *time.Time       `json:"cycle_start,omitempty"`
	CycleEnd    *time.Time       `json:"cycle_end,omitempty"`
}

// ForecastInfo represents usage forecast for a provider
type ForecastInfo struct {
	ProviderID           string     `json:"provider_id"`
	DisplayName          string     `json:"display_name"`
	CycleType            string     `json:"cycle_type"`
	CurrentUsage         float64    `json:"current_usage"`
	LimitValue           *float64   `json:"limit_value,omitempty"`
	Remaining            float64    `json:"remaining"`
	CycleEnd             *time.Time `json:"cycle_end,omitempty"`
	ForecastLimitAt      *time.Time `json:"forecast_limit_at,omitempty"`
	WillExceedBeforeReset bool      `json:"will_exceed_before_reset"`
	Confidence           float64    `json:"confidence"` // 0.0-1.0
	HoursUntilLimit      *float64   `json:"hours_until_limit,omitempty"`
	RecommendedAction    string     `json:"recommended_action,omitempty"`
}

// ProviderMetadata represents provider metadata
type ProviderMetadata struct {
	ProviderID    string            `json:"provider_id"`
	DisplayName   string            `json:"display_name"`
	AuthMethod    string            `json:"auth_method"`
	Enabled       bool              `json:"enabled"`
	CycleType     string            `json:"cycle_type"`
	LimitType     string            `json:"limit_type"`
	LimitValue    *float64          `json:"limit_value,omitempty"`
	Metrics       []string          `json:"metrics"`
	SupportedViews []string         `json:"supported_views"` // current, previous, both
	SupportedModes []string         `json:"supported_modes"` // absolute, relative, rate
	SupportedBuckets []string       `json:"supported_buckets"` // auto, hour, day, cycle
}

// ============================================================================
// Helper Functions
// ============================================================================

// calculateCycleBoundaries calculates the current cycle start/end times based on cycle type
func calculateCycleBoundaries(cycleType CycleType, now time.Time, resetAt *time.Time) (*time.Time, *time.Time) {
	var start, end time.Time

	switch cycleType {
	case CycleTypeRolling5h:
		// For rolling 5h: cycle spans from (now - 5h) to reset time or now+5h
		if resetAt != nil && resetAt.After(now) {
			start = resetAt.Add(-5 * time.Hour)
			end = *resetAt
		} else {
			start = now.Add(-5 * time.Hour)
			end = now.Add(5 * time.Hour)
		}
	case CycleTypeDaily:
		// Daily: midnight to midnight
		start = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		end = start.Add(24 * time.Hour)
	case CycleTypeWeekly:
		// Weekly: assume Monday start
		weekday := int(now.Weekday())
		if weekday == 0 {
			weekday = 7 // Sunday = 7
		}
		start = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		start = start.Add(-time.Duration(weekday-1) * 24 * time.Hour)
		end = start.Add(7 * 24 * time.Hour)
	case CycleTypeMonthly:
		// Monthly: billing cycle
		if resetAt != nil && resetAt.After(now) {
			// Approximate monthly cycle based on reset date
			end = *resetAt
			start = end.Add(-30 * 24 * time.Hour)
		} else {
			start = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
			end = start.AddDate(0, 1, 0)
		}
	default:
		// Default to daily
		start = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		end = start.Add(24 * time.Hour)
	}

	return &start, &end
}

// formatDuration formats a duration into human-readable string
func formatDuration(d time.Duration) string {
	if d < 0 {
		return "0m"
	}
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}

// calculatePace calculates usage pace from trend data
// Returns: current pace (per hour), baseline pace, ratio
func calculatePace(data []TrendDataPoint) (currentPace, baselinePace, ratio float64) {
	if len(data) < 2 {
		return 0, 0, 0
	}

	// Sort by timestamp
	for i := 0; i < len(data)-1; i++ {
		for j := i + 1; j < len(data); j++ {
			if data[i].Timestamp.After(data[j].Timestamp) {
				data[i], data[j] = data[j], data[i]
			}
		}
	}

	// Calculate current pace from recent data points (last 1 hour or 3 points)
	recentPoints := 3
	if len(data) < recentPoints {
		recentPoints = len(data)
	}
	recentData := data[len(data)-recentPoints:]
	if len(recentData) >= 2 {
		timeSpan := recentData[len(recentData)-1].Timestamp.Sub(recentData[0].Timestamp).Hours()
		if timeSpan > 0 {
			valueSpan := recentData[len(recentData)-1].Value - recentData[0].Value
			currentPace = valueSpan / timeSpan
		}
	}

	// Calculate baseline pace from all data (overall average)
	totalTimeSpan := data[len(data)-1].Timestamp.Sub(data[0].Timestamp).Hours()
	if totalTimeSpan > 0 {
		totalValueSpan := data[len(data)-1].Value - data[0].Value
		baselinePace = totalValueSpan / totalTimeSpan
	}

	if baselinePace > 0 {
		ratio = currentPace / baselinePace
	} else if currentPace > 0 {
		ratio = 999 // Very high ratio if baseline is near zero
	}

	return currentPace, baselinePace, ratio
}

// forecastLimitExceedTime forecasts when the limit will be reached
func forecastLimitExceedTime(currentUsage float64, limitValue *float64, pace float64, cycleEnd *time.Time) (*time.Time, bool) {
	if limitValue == nil || *limitValue <= 0 || pace <= 0 || cycleEnd == nil {
		return nil, false
	}

	remaining := *limitValue - currentUsage
	if remaining <= 0 {
		return nil, true // Already exceeded
	}

	hoursUntilLimit := remaining / pace
	forecastTime := time.Now().Add(time.Duration(hoursUntilLimit) * time.Hour)

	willExceedBeforeReset := forecastTime.Before(*cycleEnd)
	return &forecastTime, willExceedBeforeReset
}

// getBucketSizeForCycle determines the appropriate bucket size based on cycle type
func getBucketSizeForCycle(cycleType CycleType, requestedBucket string) string {
	if requestedBucket != "auto" && requestedBucket != "" {
		return requestedBucket
	}

	switch cycleType {
	case CycleTypeRolling5h:
		return "hour"
	case CycleTypeDaily:
		return "hour"
	case CycleTypeWeekly:
		return "day"
	case CycleTypeMonthly:
		return "day"
	default:
		return "hour"
	}
}

// aggregateDataByBucket aggregates trend data by bucket size
func aggregateDataByBucket(data []TrendDataPoint, bucket string) []TrendDataPoint {
	if len(data) == 0 {
		return data
	}

	// Sort by timestamp
	for i := 0; i < len(data)-1; i++ {
		for j := i + 1; j < len(data); j++ {
			if data[i].Timestamp.After(data[j].Timestamp) {
				data[i], data[j] = data[j], data[i]
			}
		}
	}

	if bucket == "hour" {
		return aggregateByHour(data)
	} else if bucket == "day" {
		return aggregateByDay(data)
	}

	return data
}

func aggregateByHour(data []TrendDataPoint) []TrendDataPoint {
	bucketMap := make(map[time.Time]float64)

	for _, dp := range data {
		hour := dp.Timestamp.Truncate(time.Hour)
		bucketMap[hour] = dp.Value // Take latest value for the hour
	}

	var result []TrendDataPoint
	for t, v := range bucketMap {
		result = append(result, TrendDataPoint{
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

func aggregateByDay(data []TrendDataPoint) []TrendDataPoint {
	bucketMap := make(map[time.Time]float64)

	for _, dp := range data {
		day := time.Date(dp.Timestamp.Year(), dp.Timestamp.Month(), dp.Timestamp.Day(), 0, 0, 0, 0, dp.Timestamp.Location())
		bucketMap[day] = dp.Value // Take latest value for the day
	}

	var result []TrendDataPoint
	for t, v := range bucketMap {
		result = append(result, TrendDataPoint{
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
	result := make(map[string]*CurrentCycleInfo)

	for _, p := range providers {
		cycleConfig := getProviderCycleConfig(p.Name)

		info := &CurrentCycleInfo{
			ProviderID:   p.Name,
			DisplayName:   getDisplayName(p.Name, s.registry),
			Enabled:       p.Enabled,
			CycleType:     string(cycleConfig.CycleType),
			LimitType:     string(cycleConfig.LimitType),
			LimitValue:    cycleConfig.LimitValue,
			LastUpdated:   p.LastRun,
			Error:         p.LastError,
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
			if cycleConfig.CycleType == CycleTypeRolling5h && snap.Metric == "session" {
				primarySnapshot = snap
				break
			}
			if cycleConfig.CycleType == CycleTypeMonthly && (snap.Metric == "premium_interactions" || snap.Metric == "chat") {
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
			info.CycleStart, info.CycleEnd = calculateCycleBoundaries(
				cycleConfig.CycleType,
				now,
				primarySnapshot.ResetAt,
			)

			// Calculate time remaining
			if info.CycleEnd != nil {
				info.TimeRemaining = formatDuration(info.CycleEnd.Sub(now))
			}

			// Get trend data for pace calculation
			if info.CycleStart != nil {
				startTime := *info.CycleStart
				if startTime.Before(now.Add(-30 * 24 * time.Hour)) {
					startTime = now.Add(-30 * 24 * time.Hour)
				}
				trendData, _ := s.store.GetUsageTrends(p.ID, primarySnapshot.Metric, startTime, now)
				if len(trendData) > 0 {
					points := make([]TrendDataPoint, len(trendData))
					for i, td := range trendData {
						points[i] = TrendDataPoint{
							Timestamp: td.CollectedAt,
							Value:     td.Used,
							Metric:    td.Metric,
						}
					}
					info.CurrentPace, info.BaselinePace, info.PaceVsBaselineRatio = calculatePace(points)
				}
			}

			// Forecast limit exceedance
			if info.CurrentPace > 0 && info.CycleEnd != nil {
				info.ForecastLimitAt, info.WillExceedBeforeReset = forecastLimitExceedTime(
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

	cycleConfig := getProviderCycleConfig(p.Name)
	now := time.Now()

	// Determine primary metric based on cycle type
	var primaryMetric string
	switch cycleConfig.CycleType {
	case CycleTypeRolling5h:
		primaryMetric = "session"
	case CycleTypeMonthly:
		primaryMetric = "premium_interactions"
	default:
		primaryMetric = ""
	}

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

	// Get trend data
	snapshots, err := s.store.GetUsageTrends(p.ID, primaryMetric, startTime, endTime)
	if err != nil {
		s.jsonError(w, "Failed to get trend data", nethttp.StatusInternalServerError)
		return
	}

	// Convert to trend points
	var points []TrendDataPoint
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
			// Would require calculating difference from previous point
		}

		points = append(points, TrendDataPoint{
			Timestamp: snap.CollectedAt,
			Value:     value,
			Metric:    snap.Metric,
		})
	}

	// Apply bucket aggregation
	bucketSize := getBucketSizeForCycle(cycleConfig.CycleType, bucket)
	points = aggregateDataByBucket(points, bucketSize)

	result := ProviderTrends{
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
	var result []ForecastInfo

	for _, p := range providers {
		// Filter by provider_id if specified
		if providerID != "" && p.Name != providerID {
			continue
		}

		cycleConfig := getProviderCycleConfig(p.Name)

		info := ForecastInfo{
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
			if cycleConfig.CycleType == CycleTypeRolling5h && snap.Metric == "session" {
				primarySnapshot = snap
				break
			}
			if cycleConfig.CycleType == CycleTypeMonthly && (snap.Metric == "premium_interactions" || snap.Metric == "chat") {
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
			_, cycleEnd := calculateCycleBoundaries(cycleConfig.CycleType, now, primarySnapshot.ResetAt)
			info.CycleEnd = cycleEnd

			// Get trend data for forecasting
			if cycleEnd != nil {
				startTime := cycleEnd.Add(-30 * 24 * time.Hour)
				trendData, _ := s.store.GetUsageTrends(p.ID, primarySnapshot.Metric, startTime, now)
				if len(trendData) > 0 {
					points := make([]TrendDataPoint, len(trendData))
					for i, td := range trendData {
						points[i] = TrendDataPoint{
							Timestamp: td.CollectedAt,
							Value:     td.Used,
							Metric:    td.Metric,
						}
					}
					currentPace, _, _ := calculatePace(points)

					// Calculate forecast
					info.ForecastLimitAt, info.WillExceedBeforeReset = forecastLimitExceedTime(
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
			} else if info.Remaining < (*info.LimitValue * 0.2) {
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
// GET /api/providers (extended with cycle metadata)
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

	var result []ProviderMetadata

	for _, p := range providers {
		cycleConfig := getProviderCycleConfig(p.Name)

		meta := ProviderMetadata{
			ProviderID:     p.Name,
			DisplayName:    getDisplayName(p.Name, s.registry),
			AuthMethod:     "unknown",
			Enabled:        p.Enabled,
			CycleType:      string(cycleConfig.CycleType),
			LimitType:      string(cycleConfig.LimitType),
			LimitValue:     cycleConfig.LimitValue,
			SupportedViews: []string{"current", "previous", "both"},
			SupportedModes: []string{"absolute", "relative", "rate"},
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

// ============================================================================
// Helper Functions
// ============================================================================

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