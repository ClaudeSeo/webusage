package http

import (
	"math"
	"net/http"
	"sort"
	"time"

	"github.com/ClaudeSeo/webusage/internal/store"
)

var activityLocation = mustLoadActivityLocation()

func mustLoadActivityLocation() *time.Location {
	loc, err := time.LoadLocation("Asia/Seoul")
	if err != nil {
		return time.FixedZone("KST", 9*60*60)
	}
	return loc
}

// ActivityGap describes a period in which one or more collection runs were
// expected from the configured interval but no unique collection timestamp was
// persisted.
type ActivityGap struct {
	From             time.Time `json:"from"`
	To               time.Time `json:"to"`
	DurationSeconds  int64     `json:"duration_seconds"`
	MissingIntervals int       `json:"missing_intervals"`
}

// ActivityDay is a convenient grouped view over the dense flat cells. The
// flat cells remain the canonical response field so clients can render a fixed
// 14x24 grid without inventing missing buckets.
type ActivityDay struct {
	Date          string                       `json:"date"`
	Cells         []store.SnapshotActivityCell `json:"cells"`
	SnapshotCount int                          `json:"snapshot_count"`
	Active        bool                         `json:"active"`
}

// ActivityResponse is additive to the legacy /api/heatmap response. It is
// intentionally count-based: usage values are not meaningful for collection
// coverage and duplicate metric rows must not masquerade as separate runs.
type ActivityResponse struct {
	Timezone                  string                       `json:"timezone"`
	StartDate                 string                       `json:"start_date"`
	EndDate                   string                       `json:"end_date"`
	Days                      []ActivityDay                `json:"days"`
	Cells                     []store.SnapshotActivityCell `json:"cells"`
	TotalSnapshots            int                          `json:"total_snapshots"`
	UniqueCollections         int                          `json:"unique_collections"`
	TotalCollections          int                          `json:"total_collections"`
	ActiveDays                int                          `json:"active_days"`
	CollectionIntervalSeconds int64                        `json:"collection_interval_seconds"`
	ExpectedIntervalSeconds   int64                        `json:"expected_interval_seconds"`
	ExpectedCollections       int                          `json:"expected_collections"`
	GapCount                  int                          `json:"gap_count"`
	Gaps                      []ActivityGap                `json:"gaps"`
}

// SetCollectionInterval injects the runtime collector interval used by the
// activity response's expected-run and gap calculations.
func (s *Server) SetCollectionInterval(interval time.Duration) {
	if interval <= 0 {
		return
	}
	s.collectionInterval = interval
}

func (s *Server) currentCollectionInterval() time.Duration {
	if s.collectionInterval <= 0 {
		return 15 * time.Minute
	}
	return s.collectionInterval
}

// handleAPIActivity returns a KST-aligned 14-day by 24-hour snapshot coverage
// grid. An optional end query parameter (RFC3339) makes deterministic clients
// and tests able to pin the right edge; normal dashboard requests use today in
// Asia/Seoul.
func (s *Server) handleAPIActivity(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	endLocal := time.Now().In(activityLocation)
	endProvided := false
	endIsBoundary := false
	if endValue := r.URL.Query().Get("end"); endValue != "" {
		parsed, err := time.Parse(time.RFC3339, endValue)
		if err != nil {
			s.jsonError(w, "invalid end timestamp", http.StatusBadRequest)
			return
		}
		endLocal = parsed.In(activityLocation)
		endProvided = true
		endIsBoundary = endLocal.Hour() == 0 && endLocal.Minute() == 0 && endLocal.Second() == 0 && endLocal.Nanosecond() == 0
	}
	endLocal = time.Date(endLocal.Year(), endLocal.Month(), endLocal.Day(), 0, 0, 0, 0, activityLocation)
	if !endProvided || !endIsBoundary {
		endLocal = endLocal.AddDate(0, 0, 1)
	}
	startLocal := endLocal.AddDate(0, 0, -14)

	coverage, err := s.store.GetSnapshotActivityCoverage(startLocal, endLocal, activityLocation)
	if err != nil {
		s.jsonError(w, "failed to get activity coverage", http.StatusInternalServerError)
		return
	}
	response := buildActivityResponse(coverage, s.currentCollectionInterval())
	s.jsonResponse(w, response)
}

func buildActivityResponse(coverage *store.SnapshotActivityCoverage, interval time.Duration) ActivityResponse {
	windowStart, windowEnd := coverage.StartAt, coverage.EndAt
	if windowStart.IsZero() || windowEnd.IsZero() {
		windowStart = time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC)
		windowEnd = windowStart.Add(14 * 24 * time.Hour)
	}
	windowDuration := windowEnd.Sub(windowStart)
	expectedCollections := 0
	if interval > 0 {
		expectedCollections = int(math.Ceil(windowDuration.Seconds() / interval.Seconds()))
	}
	response := ActivityResponse{
		Timezone:                  coverage.Timezone,
		StartDate:                 coverage.StartDate,
		EndDate:                   coverage.EndDate,
		Cells:                     coverage.Cells,
		TotalSnapshots:            coverage.TotalSnapshots,
		UniqueCollections:         coverage.UniqueCollections,
		TotalCollections:          coverage.UniqueCollections,
		ActiveDays:                coverage.ActiveDays,
		CollectionIntervalSeconds: int64(interval / time.Second),
		ExpectedIntervalSeconds:   int64(interval / time.Second),
		ExpectedCollections:       expectedCollections,
		Gaps:                      []ActivityGap{},
	}

	byDay := make(map[string]*ActivityDay)
	for _, cell := range coverage.Cells {
		day := byDay[cell.Date]
		if day == nil {
			day = &ActivityDay{Date: cell.Date, Cells: make([]store.SnapshotActivityCell, 0, 24)}
			byDay[cell.Date] = day
			response.Days = append(response.Days, *day)
		}
		day.Cells = append(day.Cells, cell)
		day.SnapshotCount += cell.SnapshotCount
		day.Active = day.Active || cell.SnapshotCount > 0
	}
	// The map values above are pointers while the response slice stores values;
	// rebuild it after all cells are accumulated.
	response.Days = response.Days[:0]
	dayKeys := make([]string, 0, len(byDay))
	for date := range byDay {
		dayKeys = append(dayKeys, date)
	}
	sort.Strings(dayKeys)
	for _, date := range dayKeys {
		response.Days = append(response.Days, *byDay[date])
	}

	times := append([]time.Time(nil), coverage.CollectionTimes...)
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
	if interval > 0 {
		intervalSeconds := interval.Seconds()
		appendGap := func(from, to time.Time, missing int) {
			if missing < 1 || !to.After(from) {
				return
			}
			response.Gaps = append(response.Gaps, ActivityGap{
				From:             from,
				To:               to,
				DurationSeconds:  int64(to.Sub(from) / time.Second),
				MissingIntervals: missing,
			})
		}
		if len(times) == 0 {
			// No anchor exists, so the entire requested window is a gap and
			// every expected collection interval is missing.
			appendGap(windowStart, windowEnd, response.ExpectedCollections)
		} else {
			// A small tolerance avoids reporting a gap when providers in one
			// collector pass finish a few milliseconds apart.
			leading := times[0].Sub(windowStart)
			if leading.Seconds() > intervalSeconds+2 {
				appendGap(windowStart, times[0], int(math.Ceil(leading.Seconds()/intervalSeconds))-1)
			}
			for i := 1; i < len(times); i++ {
				delta := times[i].Sub(times[i-1])
				if delta.Seconds() <= intervalSeconds+2 {
					continue
				}
				appendGap(times[i-1].Add(interval), times[i], int(math.Ceil(delta.Seconds()/intervalSeconds))-1)
			}
			trailing := windowEnd.Sub(times[len(times)-1])
			if trailing.Seconds() > intervalSeconds+2 {
				appendGap(times[len(times)-1].Add(interval), windowEnd, int(math.Ceil(trailing.Seconds()/intervalSeconds))-1)
			}
		}
	}
	response.GapCount = len(response.Gaps)
	return response
}
