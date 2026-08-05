package store

import (
	"fmt"
	"sort"
	"time"
)

// SnapshotActivityCell is the count of persisted snapshots for one local
// calendar hour. CollectionCount is the number of distinct collection runs in
// that hour; a run can write several metric rows, so it is intentionally
// separate from SnapshotCount.
type SnapshotActivityCell struct {
	Date            string `json:"date"`
	Hour            int    `json:"hour"`
	SnapshotCount   int    `json:"snapshot_count"`
	CollectionCount int    `json:"collection_count"`
}

// SnapshotActivityCoverage contains a dense hour grid and the collection
// timestamps used by the HTTP layer to identify gaps against its configured
// interval.
type SnapshotActivityCoverage struct {
	Timezone          string                 `json:"timezone"`
	StartDate         string                 `json:"start_date"`
	EndDate           string                 `json:"end_date"`
	Cells             []SnapshotActivityCell `json:"cells"`
	TotalSnapshots    int                    `json:"total_snapshots"`
	UniqueCollections int                    `json:"unique_collections"`
	ActiveDays        int                    `json:"active_days"`
	CollectionTimes   []time.Time            `json:"-"`
	StartAt           time.Time              `json:"-"`
	EndAt             time.Time              `json:"-"`
}

// Cell returns the requested cell from a dense coverage result. It returns a
// zero-valued cell when the date/hour is outside the requested range.
func (c *SnapshotActivityCoverage) Cell(date string, hour int) SnapshotActivityCell {
	if c == nil || hour < 0 || hour > 23 {
		return SnapshotActivityCell{Date: date, Hour: hour}
	}
	for _, cell := range c.Cells {
		if cell.Date == date && cell.Hour == hour {
			return cell
		}
	}
	return SnapshotActivityCell{Date: date, Hour: hour}
}

// GetSnapshotActivityCoverage reads a dense local-time hour grid. start is
// inclusive and end is exclusive. The query itself remains UTC because
// collected_at is normalized to UTC at persistence time; conversion to the
// requested display zone happens after scanning rows.
func (s *Store) GetSnapshotActivityCoverage(start, end time.Time, loc *time.Location) (*SnapshotActivityCoverage, error) {
	if loc == nil {
		loc = time.UTC
	}
	if end.Before(start) || end.Equal(start) {
		return nil, fmt.Errorf("activity range must have a positive duration")
	}
	startUTC, endUTC := start.UTC(), end.UTC()
	rows, err := s.db.Query(`
		SELECT collected_at
		FROM usage_snapshots
		WHERE collected_at >= ? AND collected_at < ?
		ORDER BY collected_at ASC
	`, startUTC, endUTC)
	if err != nil {
		return nil, fmt.Errorf("querying snapshot activity: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type cellKey struct {
		date string
		hour int
	}
	counts := make(map[cellKey]int)
	collections := make(map[cellKey]map[int64]struct{})
	unique := make(map[int64]struct{})
	var total int
	var collectionTimes []time.Time
	for rows.Next() {
		var collectedAt time.Time
		if err := rows.Scan(&collectedAt); err != nil {
			return nil, fmt.Errorf("scanning snapshot activity: %w", err)
		}
		collectedAt = collectedAt.UTC()
		local := collectedAt.In(loc)
		key := cellKey{date: local.Format("2006-01-02"), hour: local.Hour()}
		epoch := collectedAt.UnixNano()
		counts[key]++
		if collections[key] == nil {
			collections[key] = make(map[int64]struct{})
		}
		collections[key][epoch] = struct{}{}
		if _, exists := unique[epoch]; !exists {
			unique[epoch] = struct{}{}
			collectionTimes = append(collectionTimes, collectedAt)
		}
		total++
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating snapshot activity: %w", err)
	}
	sort.Slice(collectionTimes, func(i, j int) bool { return collectionTimes[i].Before(collectionTimes[j]) })

	startLocal := start.In(loc)
	endLocal := end.In(loc)
	// The display range is expected to be midnight-aligned. For a caller that
	// supplies arbitrary times, include each local calendar date touched by the
	// half-open interval and still return every 24-hour cell for that date.
	firstDate := time.Date(startLocal.Year(), startLocal.Month(), startLocal.Day(), 0, 0, 0, 0, loc)
	lastDate := endLocal
	if endLocal.Hour() == 0 && endLocal.Minute() == 0 && endLocal.Second() == 0 && endLocal.Nanosecond() == 0 {
		lastDate = endLocal.AddDate(0, 0, -1)
	}
	lastDate = time.Date(lastDate.Year(), lastDate.Month(), lastDate.Day(), 0, 0, 0, 0, loc)

	coverage := &SnapshotActivityCoverage{
		Timezone:          loc.String(),
		StartDate:         firstDate.Format("2006-01-02"),
		EndDate:           lastDate.Format("2006-01-02"),
		TotalSnapshots:    total,
		UniqueCollections: len(unique),
		CollectionTimes:   collectionTimes,
		StartAt:           startUTC,
		EndAt:             endUTC,
	}
	for day := firstDate; !day.After(lastDate); day = day.AddDate(0, 0, 1) {
		dayActive := false
		date := day.Format("2006-01-02")
		for hour := 0; hour < 24; hour++ {
			key := cellKey{date: date, hour: hour}
			count := counts[key]
			collectionCount := len(collections[key])
			if count > 0 {
				dayActive = true
			}
			coverage.Cells = append(coverage.Cells, SnapshotActivityCell{
				Date:            date,
				Hour:            hour,
				SnapshotCount:   count,
				CollectionCount: collectionCount,
			})
		}
		if dayActive {
			coverage.ActiveDays++
		}
	}
	return coverage, nil
}

// GetActivityCoverage is a concise alias used by HTTP callers and external
// integrations. It preserves the store's UTC persistence and local display
// semantics of GetSnapshotActivityCoverage.
func (s *Store) GetActivityCoverage(start, end time.Time, loc *time.Location) (*SnapshotActivityCoverage, error) {
	return s.GetSnapshotActivityCoverage(start, end, loc)
}

// GetSnapshotCoverage is retained as another descriptive alias for callers
// that use "coverage" terminology.
func (s *Store) GetSnapshotCoverage(start, end time.Time, loc *time.Location) (*SnapshotActivityCoverage, error) {
	return s.GetSnapshotActivityCoverage(start, end, loc)
}
