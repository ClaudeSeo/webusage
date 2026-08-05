package http

import (
	"fmt"
	nethttp "net/http"
	"sort"
	"strings"
	"time"

	"github.com/ClaudeSeo/webusage/internal/domain"
	"github.com/ClaudeSeo/webusage/internal/store"
)

// metricSnapshotSet keeps the latest row, all trend points, and the
// deterministic G1 projection inputs together. HTTP handlers only shape the
// result; cycle, pace, reset, weak-estimate, and severity rules stay in
// internal/domain.
type metricSnapshotSet struct {
	Metric     string
	Latest     *store.UsageSnapshot
	Trend      []domain.TrendDataPoint
	Projection domain.MetricProjection
}

func orderedMetricSnapshots(s *store.Store, provider *store.Provider, snapshots []*store.UsageSnapshot) ([]*store.UsageSnapshot, error) {
	byMetric := make(map[string]*store.UsageSnapshot, len(snapshots))
	catalog := make([]string, 0, len(snapshots))
	for _, snapshot := range snapshots {
		if snapshot == nil {
			continue
		}
		byMetric[snapshot.Metric] = snapshot
		catalog = append(catalog, snapshot.Metric)
	}
	preference, err := s.GetMetricPreference(provider.ID)
	if err != nil {
		return nil, err
	}
	items := domain.ReconcileMetricPreferences(preference.Items, catalog)
	ordered := make([]*store.UsageSnapshot, 0, len(catalog))
	seen := make(map[string]struct{}, len(catalog))
	for _, item := range items {
		// Mark catalog entries present in a saved preference even when hidden;
		// otherwise the fallback tail would accidentally re-enable them.
		if item.Available {
			seen[item.Metric] = struct{}{}
		}
		if !item.Available || !item.Visible {
			continue
		}
		if snapshot := byMetric[item.Metric]; snapshot != nil {
			ordered = append(ordered, snapshot)
		}
	}
	// Unknown/new metrics remain available even before a preference row is
	// saved. The store's latest query is stable by metric name, so this tail is
	// deterministic as well.
	for _, snapshot := range snapshots {
		if snapshot == nil {
			continue
		}
		if _, ok := seen[snapshot.Metric]; ok {
			continue
		}
		ordered = append(ordered, snapshot)
		seen[snapshot.Metric] = struct{}{}
	}
	return ordered, nil
}

func loadMetricSnapshotSets(s *store.Store, provider *store.Provider, now time.Time) ([]metricSnapshotSet, error) {
	latest, err := s.GetLatestUsageByProvider(provider.ID)
	if err != nil {
		return nil, err
	}
	ordered, err := orderedMetricSnapshots(s, provider, latest)
	if err != nil {
		return nil, err
	}
	sets := make([]metricSnapshotSet, 0, len(ordered))
	for _, snapshot := range ordered {
		start := now.Add(-30 * 24 * time.Hour)
		trendRows, err := s.GetUsageTrends(provider.ID, snapshot.Metric, start, now)
		if err != nil {
			trendRows = nil
		}
		trend := make([]domain.TrendDataPoint, 0, len(trendRows))
		for _, row := range trendRows {
			trend = append(trend, domain.TrendDataPoint{Timestamp: row.CollectedAt, Value: row.Used, Metric: row.Metric})
		}
		cycle := domain.ResolveMetricCycleType(provider.Name, snapshot.Metric, domain.GetProviderCycleConfig(provider.Name).CycleType)
		projection := domain.ProjectMetric(domain.MetricProjectionInput{
			CycleType: cycle,
			Limit:     snapshot.Limit,
			ResetAt:   snapshot.ResetAt,
			Now:       now,
			Snapshots: trend,
		})
		sets = append(sets, metricSnapshotSet{Metric: snapshot.Metric, Latest: snapshot, Trend: trend, Projection: projection})
	}
	return sets, nil
}

func primaryMetricSet(provider *store.Provider, sets []metricSnapshotSet) *metricSnapshotSet {
	if len(sets) == 0 {
		return nil
	}
	config := domain.GetProviderCycleConfig(provider.Name)
	for i := range sets {
		if sets[i].Metric == config.PrimaryMetric {
			return &sets[i]
		}
	}
	for i := range sets {
		if sets[i].Latest != nil && sets[i].Latest.Limit != nil {
			return &sets[i]
		}
	}
	return &sets[0]
}

func metricNames(sets []metricSnapshotSet) []string {
	names := make([]string, 0, len(sets))
	for _, set := range sets {
		names = append(names, set.Metric)
	}
	return names
}

func metricNamesFromSnapshots(snapshots []*store.UsageSnapshot) []string {
	names := make([]string, 0, len(snapshots))
	for _, snapshot := range snapshots {
		if snapshot != nil {
			names = append(names, snapshot.Metric)
		}
	}
	return names
}

func metricProjectionJSON(set metricSnapshotSet) map[string]interface{} {
	p := set.Projection
	latest := set.Latest
	result := map[string]interface{}{
		"metric":                   set.Metric,
		"cycle_type":               string(p.CycleType),
		"cycle_label":              p.CycleLabel,
		"has_current":              p.HasCurrent,
		"current_usage":            p.CurrentUsage,
		"usage_percent":            p.CurrentPercent,
		"current_percent":          p.CurrentPercent,
		"has_pace":                 p.HasPace,
		"pace_per_hour":            p.PacePerHour,
		"current_pace":             p.PacePerHour,
		"observation_window_hours": p.ObservationWindowHours,
		"has_projection":           p.HasProjection,
		"has_forecast":             p.HasForecast,
		"forecast_usable":          p.ForecastUsable,
		"weak_estimate":            p.WeakEstimate,
		"severity":                 string(p.Severity),
		"time_remaining":           p.TimeRemainingText,
		"hours_until_reset":        p.HoursUntilReset,
		"projected_usage":          p.ProjectedUsage,
		"projected_percent":        p.ProjectedPercent,
		"cycle_start":              p.CycleStart,
		"cycle_end":                p.CycleEnd,
		"last_updated":             nil,
	}
	if latest != nil {
		result["limit"] = latest.Limit
		result["limit_value"] = latest.Limit
		result["reset_at"] = latest.ResetAt
		result["last_updated"] = latest.CollectedAt
	}
	return result
}

func safeCollectionError(err *string) *string {
	if err == nil || *err == "" {
		return nil
	}
	message := "collection failed"
	return &message
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
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Timestamp.Before(sorted[j].Timestamp)
	})

	switch bucket {
	case "hour":
		return aggregateByHour(sorted)
	case "day":
		return aggregateByDay(sorted)
	default:
		return sorted
	}
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
	sort.Slice(result, func(i, j int) bool {
		return result[i].Timestamp.Before(result[j].Timestamp)
	})

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
	sort.Slice(result, func(i, j int) bool {
		return result[i].Timestamp.Before(result[j].Timestamp)
	})

	return result
}

// ============================================================================
// API Handlers
// ============================================================================

// handleAPICurrent returns current cycle-aware usage for all providers
// GET /api/current
func (s *Server) handleAPICurrentLegacy(w nethttp.ResponseWriter, r *nethttp.Request) {
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

		cycleConfig := domain.GetProviderCycleConfig(p.Name)

		// Get latest snapshots for this provider
		snapshots, err := s.store.GetLatestUsageByProvider(p.ID)
		if err != nil {
			s.logger.Warn("Failed to get latest snapshots", "provider", p.Name, "error", err)
			continue
		}

		primaryMetric := cycleConfig.PrimaryMetric

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
			"baseline_pace":            0.0,
			"pace_vs_baseline_ratio":   0.0,
		}

		if primarySnapshot != nil {
			info["current_usage"] = primarySnapshot.Used
			if primarySnapshot.Limit != nil && *primarySnapshot.Limit > 0 {
				info["usage_percent"] = (primarySnapshot.Used / *primarySnapshot.Limit) * 100
				info["limit_value"] = *primarySnapshot.Limit
			}

			// Calculate cycle boundaries
			info["cycle_start"], info["cycle_end"] = domain.CalculateCycleBoundaries(
				cycleConfig.CycleType,
				now,
				primarySnapshot.ResetAt,
			)

			// Calculate time remaining
			if cycleEnd, ok := info["cycle_end"].(*time.Time); ok && cycleEnd != nil {
				info["time_remaining"] = domain.FormatDuration(cycleEnd.Sub(now))
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
					info["current_pace"], info["baseline_pace"], info["pace_vs_baseline_ratio"] = domain.CalculatePace(points)
				}
			}

			// Forecast limit exceedance
			currentPace, _ := info["current_pace"].(float64)
			limitValue, _ := info["limit_value"].(*float64)
			cycleEnd, _ := info["cycle_end"].(*time.Time)
			if currentPace > 0 && limitValue != nil && cycleEnd != nil {
				info["forecast_limit_at"], info["will_exceed_before_reset"] = domain.ForecastLimitExceedTime(
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

// handleAPICurrent returns the legacy provider-level fields together with a
// per-metric projection map. The metric map is additive, so existing clients
// can keep decoding CurrentCycleInfo while the dashboard uses richer data.
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
	now := time.Now().UTC()
	result := make(map[string]interface{}, len(providers))
	for _, provider := range providers {
		if !provider.Enabled {
			continue
		}
		sets, err := loadMetricSnapshotSets(s.store, provider, now)
		if err != nil {
			s.logger.Warn("Failed to get metric snapshots", "provider", provider.Name, "error", err)
			continue
		}
		cycleConfig := domain.GetProviderCycleConfig(provider.Name)
		metrics := make(map[string]interface{}, len(sets))
		for _, set := range sets {
			metrics[set.Metric] = metricProjectionJSON(set)
		}
		info := map[string]interface{}{
			"provider_id":              provider.Name,
			"display_name":             getDisplayName(provider.Name),
			"enabled":                  provider.Enabled,
			"cycle_type":               string(cycleConfig.CycleType),
			"limit_type":               string(cycleConfig.LimitType),
			"current_usage":            0.0,
			"usage_percent":            0.0,
			"will_exceed_before_reset": false,
			"current_pace":             0.0,
			"baseline_pace":            0.0,
			"pace_vs_baseline_ratio":   0.0,
			"metrics":                  metrics,
			"available_metrics":        metricNames(sets),
			"collection_error":         nil,
		}
		if safeErr := safeCollectionError(provider.LastError); safeErr != nil {
			info["collection_error"] = *safeErr
			info["error"] = *safeErr
		}
		primary := primaryMetricSet(provider, sets)
		if primary != nil {
			projection := primary.Projection
			latest := primary.Latest
			info["primary_metric"] = primary.Metric
			info["current_usage"] = projection.CurrentUsage
			info["usage_percent"] = projection.CurrentPercent
			info["cycle_start"] = projection.CycleStart
			info["cycle_end"] = projection.CycleEnd
			info["time_remaining"] = projection.TimeRemainingText
			info["current_pace"] = projection.PacePerHour
			if latest != nil {
				info["limit_value"] = latest.Limit
				info["last_updated"] = latest.CollectedAt.Format(time.RFC3339)
			}
			if len(primary.Trend) > 1 {
				_, baseline, ratio := domain.CalculatePace(primary.Trend)
				info["baseline_pace"] = baseline
				info["pace_vs_baseline_ratio"] = ratio
			}
			if projection.HasPace && latest != nil && latest.Limit != nil && projection.CycleEnd != nil {
				forecastAt, willExceed := domain.ForecastLimitExceedTime(latest.Used, latest.Limit, projection.PacePerHour, projection.CycleEnd)
				info["forecast_limit_at"] = forecastAt
				info["will_exceed_before_reset"] = willExceed
			}
		}
		result[provider.Name] = info
	}
	s.jsonResponse(w, result)
}

// handleAPITrends returns cycle-aware trend data
// GET /api/trends?provider_id=&range=&view=&mode=&bucket=
// If provider_id is omitted, return trend data for all active providers based on range
func (s *Server) handleAPITrends(w nethttp.ResponseWriter, r *nethttp.Request) {
	if r.Method != nethttp.MethodGet {
		nethttp.Error(w, "Method not allowed", nethttp.StatusMethodNotAllowed)
		return
	}

	providerID := r.URL.Query().Get("provider_id")
	rangeValue := r.URL.Query().Get("range")
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

	// If provider_id is omitted, return all active providers
	if providerID == "" {
		s.handleAllProvidersTrends(w, r, rangeValue, view, mode, bucket)
		return
	}

	// Get provider from store
	p, err := s.store.GetProviderByName(providerID)
	if err != nil {
		s.jsonError(w, fmt.Sprintf("Provider '%s' not found", providerID), nethttp.StatusNotFound)
		return
	}

	cycleConfig := domain.GetProviderCycleConfig(p.Name)
	now := time.Now().UTC()
	latestSnapshots, _ := s.store.GetLatestUsageByProvider(p.ID)
	requestedMetric := r.URL.Query().Get("metric")
	primaryMetric := requestedMetric
	if primaryMetric == "" {
		primaryMetric = cycleConfig.PrimaryMetric
	}
	if primaryMetric == "" {
		for _, snapshot := range latestSnapshots {
			if snapshot != nil {
				primaryMetric = snapshot.Metric
				break
			}
		}
	}
	var latestMetric *store.UsageSnapshot
	for _, snapshot := range latestSnapshots {
		if snapshot != nil && snapshot.Metric == primaryMetric {
			latestMetric = snapshot
			break
		}
	}
	metricCycle := domain.ResolveMetricCycleType(p.Name, primaryMetric, cycleConfig.CycleType)

	// Calculate time range based on view
	var startTime, endTime time.Time
	cycleStart, cycleEnd := domain.CalculateMetricCycleBoundaries(metricCycle, now, func() *time.Time {
		if latestMetric == nil {
			return nil
		}
		return latestMetric.ResetAt
	}())

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
			// Emit a non-negative consecutive delta. A decrease marks a
			// cycle reset, so it contributes zero rather than borrowing a
			// negative rate from the previous cycle. The first point has no
			// predecessor and is therefore explicitly zero.
			if i := len(points); i == 0 {
				value = 0
			} else {
				previous := snapshots[i-1].Used
				value -= previous
				if value < 0 {
					value = 0
				}
			}
		}

		points = append(points, domain.TrendDataPoint{
			Timestamp: snap.CollectedAt,
			Value:     value,
			Metric:    snap.Metric,
		})
	}

	// Apply bucket aggregation
	bucketSize := getBucketSizeForCycle(metricCycle, bucket)
	points = aggregateDataByBucket(points, bucketSize)

	result := map[string]interface{}{
		"provider_id":       providerID,
		"cycle_type":        string(metricCycle),
		"view":              view,
		"mode":              mode,
		"bucket":            bucketSize,
		"data":              points,
		"metric":            primaryMetric,
		"available_metrics": metricNamesFromSnapshots(latestSnapshots),
	}
	if cycleStart != nil {
		result["cycle_start"] = cycleStart
	}
	if cycleEnd != nil {
		result["cycle_end"] = cycleEnd
	}

	s.jsonResponse(w, result)
}

// handleAPIForecast returns usage forecast for all providers or specific provider
// GET /api/forecast?provider_id=
func (s *Server) handleAPIForecastLegacy(w nethttp.ResponseWriter, r *nethttp.Request) {
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

		cycleConfig := domain.GetProviderCycleConfig(p.Name)
		snapshots, err := s.store.GetLatestUsageByProvider(p.ID)
		if err != nil {
			continue
		}

		primaryMetric := cycleConfig.PrimaryMetric

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
		cycleStart, cycleEnd := domain.CalculateCycleBoundaries(cycleConfig.CycleType, now, primarySnapshot.ResetAt)
		if cycleEnd == nil {
			continue
		}

		forecast := map[string]interface{}{
			"provider_id":    p.Name,
			"cycle_type":     string(cycleConfig.CycleType),
			"current_usage":  primarySnapshot.Used,
			"cycle_end":      cycleEnd,
			"time_remaining": domain.FormatDuration(cycleEnd.Sub(now)),
			"confidence":     0.8, // Default confidence
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
					currentPace, baselinePace, _ := domain.CalculatePace(points)
					forecast["current_pace"] = currentPace
					forecast["baseline_pace"] = baselinePace

					// Forecast exceed time
					forecastAt, willExceed := domain.ForecastLimitExceedTime(
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

// handleAPIForecast returns provider-compatible forecast objects and additive
// per-metric forecasts. Limit pointers remain typed throughout construction so
// callers never lose the current-limit value through an interface assertion.
func (s *Server) handleAPIForecast(w nethttp.ResponseWriter, r *nethttp.Request) {
	if r.Method != nethttp.MethodGet {
		nethttp.Error(w, "Method not allowed", nethttp.StatusMethodNotAllowed)
		return
	}
	providerFilter := r.URL.Query().Get("provider_id")
	providers, err := s.store.ListProviders()
	if err != nil {
		s.jsonError(w, "Failed to list providers", nethttp.StatusInternalServerError)
		return
	}
	now := time.Now().UTC()
	forecasts := make([]map[string]interface{}, 0, len(providers))
	for _, provider := range providers {
		if !provider.Enabled || (providerFilter != "" && provider.Name != providerFilter) {
			continue
		}
		sets, err := loadMetricSnapshotSets(s.store, provider, now)
		if err != nil {
			continue
		}
		safeErr := safeCollectionError(provider.LastError)
		primary := primaryMetricSet(provider, sets)
		if primary == nil {
			if safeErr != nil {
				forecasts = append(forecasts, map[string]interface{}{
					"provider_id":       provider.Name,
					"display_name":      getDisplayName(provider.Name),
					"cycle_type":        string(domain.GetProviderCycleConfig(provider.Name).CycleType),
					"current_usage":     0.0,
					"remaining":         0.0,
					"confidence":        0.0,
					"metrics":           map[string]interface{}{},
					"available_metrics": []string{},
					"collection_error":  *safeErr,
					"error":             *safeErr,
				})
			}
			continue
		}
		projection := primary.Projection
		latest := primary.Latest
		if latest == nil {
			continue
		}
		forecast := map[string]interface{}{
			"provider_id":       provider.Name,
			"display_name":      getDisplayName(provider.Name),
			"cycle_type":        string(projection.CycleType),
			"primary_metric":    primary.Metric,
			"current_usage":     projection.CurrentUsage,
			"remaining":         0.0,
			"cycle_end":         projection.CycleEnd,
			"time_remaining":    projection.TimeRemainingText,
			"confidence":        forecastConfidence(projection),
			"metrics":           metricForecasts(sets),
			"available_metrics": metricNames(sets),
			"collection_error":  nil,
		}
		if latest.Limit != nil {
			forecast["limit"] = latest.Limit
			forecast["limit_value"] = latest.Limit
			forecast["remaining"] = *latest.Limit - latest.Used
			if projection.HasPace && projection.CycleEnd != nil {
				at, exceed := domain.ForecastLimitExceedTime(latest.Used, latest.Limit, projection.PacePerHour, projection.CycleEnd)
				forecast["forecast_limit_at"] = at
				forecast["will_exceed_before_reset"] = exceed
				if at != nil {
					hours := at.Sub(now).Hours()
					if hours < 0 {
						hours = 0
					}
					forecast["hours_until_limit"] = hours
				}
			}
		}
		if safeErr != nil {
			forecast["collection_error"] = *safeErr
			forecast["error"] = *safeErr
		}
		forecasts = append(forecasts, forecast)
	}
	if providerFilter != "" && len(forecasts) == 1 {
		s.jsonResponse(w, forecasts[0])
		return
	}
	s.jsonResponse(w, map[string]interface{}{"forecasts": forecasts})
}

func forecastConfidence(projection domain.MetricProjection) float64 {
	if !projection.HasForecast {
		if projection.HasPace {
			return 0.5
		}
		return 0
	}
	if projection.WeakEstimate {
		return 0.35
	}
	return 0.8
}

func metricForecasts(sets []metricSnapshotSet) map[string]interface{} {
	result := make(map[string]interface{}, len(sets))
	for _, set := range sets {
		item := metricProjectionJSON(set)
		if set.Latest != nil && set.Latest.Limit != nil && set.Projection.HasPace && set.Projection.CycleEnd != nil {
			at, exceed := domain.ForecastLimitExceedTime(set.Latest.Used, set.Latest.Limit, set.Projection.PacePerHour, set.Projection.CycleEnd)
			item["forecast_limit_at"] = at
			item["will_exceed_before_reset"] = exceed
		}
		result[set.Metric] = item
	}
	return result
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
		ProviderID       string                 `json:"provider_id"`
		DisplayName      string                 `json:"display_name"`
		AuthMethod       string                 `json:"auth_method"`
		Enabled          bool                   `json:"enabled"`
		CycleType        string                 `json:"cycle_type"`
		LimitType        string                 `json:"limit_type"`
		Metrics          []string               `json:"metrics"`
		SupportedViews   []string               `json:"supported_views"`
		SupportedModes   []string               `json:"supported_modes"`
		SupportedBuckets []string               `json:"supported_buckets"`
		MetricDetails    map[string]interface{} `json:"metric_details,omitempty"`
		LastUpdated      *time.Time             `json:"last_updated,omitempty"`
	}

	var result []ProviderMeta

	for _, p := range providers {
		cycleConfig := domain.GetProviderCycleConfig(p.Name)

		meta := ProviderMeta{
			ProviderID:       p.Name,
			DisplayName:      getDisplayName(p.Name),
			AuthMethod:       safeAuthMethod(p),
			Enabled:          p.Enabled,
			CycleType:        string(cycleConfig.CycleType),
			LimitType:        string(cycleConfig.LimitType),
			SupportedViews:   []string{"current", "previous", "both"},
			SupportedModes:   []string{"absolute", "relative", "rate"},
			SupportedBuckets: []string{"auto", "hour", "day", "cycle"},
		}

		// Get available metrics and presentation-safe projections. ConfigJSON,
		// credential paths, source URLs, and provider errors never enter this
		// response.
		sets, _ := loadMetricSnapshotSets(s.store, p, time.Now().UTC())
		meta.MetricDetails = make(map[string]interface{}, len(sets))
		for _, set := range sets {
			meta.Metrics = append(meta.Metrics, set.Metric)
			meta.MetricDetails[set.Metric] = metricProjectionJSON(set)
			if set.Latest != nil && (meta.LastUpdated == nil || set.Latest.CollectedAt.After(*meta.LastUpdated)) {
				at := set.Latest.CollectedAt
				meta.LastUpdated = &at
			}
		}

		result = append(result, meta)
	}

	s.jsonResponse(w, map[string]interface{}{
		"providers": result,
	})
}

func safeAuthMethod(provider *store.Provider) string {
	if provider == nil {
		return ""
	}
	config, err := store.UnmarshalProviderConfig(provider.ConfigJSON)
	if err == nil && config != nil {
		method := strings.TrimSpace(config.AuthMethod)
		if method != "" && !strings.ContainsAny(method, "\r\n:/\\") {
			return method
		}
	}
	switch provider.Name {
	case "kirocli":
		return "native"
	case "ollama":
		return "api_key"
	default:
		return ""
	}
}

// handleAPIHeatmap returns heatmap data aggregated by date (GitHub contribution graph style)
// GET /api/heatmap?provider_id=&range=1y
func (s *Server) handleAPIHeatmap(w nethttp.ResponseWriter, r *nethttp.Request) {
	if r.Method != nethttp.MethodGet {
		nethttp.Error(w, "Method not allowed", nethttp.StatusMethodNotAllowed)
		return
	}

	providerIDStr := r.URL.Query().Get("provider_id")
	rangeValue := r.URL.Query().Get("range")
	if rangeValue == "" {
		rangeValue = "1y"
	}

	now := time.Now().UTC()
	var startTime time.Time
	switch rangeValue {
	case "7d":
		startTime = now.Add(-7 * 24 * time.Hour)
	case "30d":
		startTime = now.Add(-30 * 24 * time.Hour)
	case "1y":
		startTime = now.Add(-365 * 24 * time.Hour)
	default:
		startTime = now.Add(-365 * 24 * time.Hour)
	}

	var providerID int64
	if providerIDStr != "" {
		p, err := s.store.GetProviderByName(providerIDStr)
		if err != nil {
			s.jsonError(w, fmt.Sprintf("Provider '%s' not found", providerIDStr), nethttp.StatusNotFound)
			return
		}
		providerID = p.ID
	}

	data, err := s.store.GetHeatmapData(providerID, startTime, now)
	if err != nil {
		s.jsonError(w, "Failed to get heatmap data", nethttp.StatusInternalServerError)
		return
	}

	s.jsonResponse(w, data)
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

func resolveAllProvidersTrendWindow(rangeValue, view string, now time.Time) (time.Time, time.Time) {
	selector := rangeValue
	if selector == "" {
		selector = view
	}

	switch selector {
	case "5h":
		return now.Add(-5 * time.Hour), now
	case "7d":
		return now.Add(-7 * 24 * time.Hour), now
	case "30d":
		return now.Add(-30 * 24 * time.Hour), now
	case "24h", "current":
		return now.Add(-24 * time.Hour), now
	default:
		return now.Add(-24 * time.Hour), now
	}
}

// handleAllProvidersTrends returns trend data for all active providers
func (s *Server) handleAllProvidersTrends(w nethttp.ResponseWriter, r *nethttp.Request, rangeValue, view, mode, bucket string) {
	providers, err := s.store.ListProviders()
	if err != nil {
		s.jsonError(w, "Failed to list providers", nethttp.StatusInternalServerError)
		return
	}

	// collected_at is stored as UTC in the DB, so compare in UTC
	now := time.Now().UTC()
	result := make(map[string]interface{})

	for _, p := range providers {
		if !p.Enabled {
			continue
		}

		cycleConfig := domain.GetProviderCycleConfig(p.Name)
		startTime, endTime := resolveAllProvidersTrendWindow(rangeValue, view, now)

		// Fetch all metric data at once (metric="" → all)
		allSnapshots, err := s.store.GetUsageTrends(p.ID, "", startTime, endTime)
		if err != nil {
			continue
		}

		// Collect the current catalog and per-metric limit info from the latest snapshot
		latestSnapshots, err := s.store.GetLatestUsageByProvider(p.ID)
		if err != nil {
			continue
		}
		metricLimits := make(map[string]*float64)
		catalog := make([]string, 0, len(latestSnapshots))
		for _, snap := range latestSnapshots {
			catalog = append(catalog, snap.Metric)
			if snap.Limit != nil {
				metricLimits[snap.Metric] = snap.Limit
			}
		}
		preference, err := s.store.GetMetricPreference(p.ID)
		if err != nil {
			continue
		}
		canonicalItems := domain.ReconcileMetricPreferences(preference.Items, catalog)

		// Group trend data by metric
		metricTrends := make(map[string][]map[string]interface{})

		for _, snap := range allSnapshots {
			metricTrends[snap.Metric] = append(metricTrends[snap.Metric], map[string]interface{}{
				"timestamp": snap.CollectedAt.Format(time.RFC3339),
				"value":     snap.Used,
				"metric":    snap.Metric,
			})
		}
		if mode == "relative" || mode == "rate" {
			for metric, trend := range metricTrends {
				if len(trend) == 0 {
					continue
				}
				baseline, _ := trend[0]["value"].(float64)
				var previous = baseline
				for index := range trend {
					value, _ := trend[index]["value"].(float64)
					if mode == "relative" {
						trend[index]["value"] = value - baseline
					} else {
						trend[index]["value"] = value - previous
					}
					previous = value
				}
				metricTrends[metric] = trend
			}
		}

		// Convert into a per-metric {trend, limit} structure
		metricsData := make(map[string]interface{})
		availableMetrics := make([]string, 0, len(canonicalItems))
		for _, item := range canonicalItems {
			if !item.Available || !item.Visible {
				continue
			}
			trend := metricTrends[item.Metric]
			if trend == nil {
				trend = []map[string]interface{}{}
			}
			availableMetrics = append(availableMetrics, item.Metric)
			metricCycle := domain.ResolveMetricCycleType(p.Name, item.Metric, cycleConfig.CycleType)
			metricsData[item.Metric] = map[string]interface{}{
				"trend":       trend,
				"limit":       metricLimits[item.Metric],
				"cycle_type":  string(metricCycle),
				"cycle_label": domain.CycleLabel(metricCycle),
			}
		}

		// Default to the first metric from the display preferences, regardless of whether range data exists.
		primaryMetric := ""
		if len(availableMetrics) > 0 {
			primaryMetric = availableMetrics[0]
		}

		result[p.Name] = map[string]interface{}{
			"display_name":      getDisplayName(p.Name),
			"cycle_type":        string(cycleConfig.CycleType),
			"limit_type":        string(cycleConfig.LimitType),
			"available_metrics": availableMetrics,
			"primary_metric":    primaryMetric,
			"metrics":           metricsData,
		}
	}

	s.jsonResponse(w, result)
}
