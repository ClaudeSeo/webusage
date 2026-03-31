package store

import (
	"database/sql"
	"time"
)

// UsageSnapshot represents a single usage data point
type UsageSnapshot struct {
	ID          int64      `json:"id"`
	ProviderID  int64      `json:"provider_id"`
	Metric      string     `json:"metric"`
	Used        float64    `json:"used"`
	Limit       *float64   `json:"limit,omitempty"`
	ResetAt     *time.Time `json:"reset_at,omitempty"`
	CollectedAt time.Time  `json:"collected_at"`
	RawJSON     string     `json:"raw_json"`
}

// CreateUsageSnapshot inserts a new usage data point
func (s *Store) CreateUsageSnapshot(snapshot *UsageSnapshot) (int64, error) {
	result, err := s.db.Exec(`
		INSERT INTO usage_snapshots
		(provider_id, metric, used, "limit", reset_at, collected_at, raw_json)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, snapshot.ProviderID, snapshot.Metric, snapshot.Used,
		snapshot.Limit, snapshot.ResetAt, snapshot.CollectedAt, snapshot.RawJSON)
	if err != nil {
		return 0, err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}

	return id, nil
}

// CreateUsageSnapshots inserts multiple usage data points in a transaction
func (s *Store) CreateUsageSnapshots(snapshots []*UsageSnapshot) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO usage_snapshots
		(provider_id, metric, used, "limit", reset_at, collected_at, raw_json)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, snap := range snapshots {
		_, err := stmt.Exec(
			snap.ProviderID, snap.Metric, snap.Used,
			snap.Limit, snap.ResetAt, snap.CollectedAt, snap.RawJSON,
		)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// CreateUsageSnapshotsIdempotent inserts snapshots with idempotency check
// Prevents duplicate entries for the same provider+metric+collected_at (within 1 second)
func (s *Store) CreateUsageSnapshotsIdempotent(snapshots []*UsageSnapshot) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT OR IGNORE INTO usage_snapshots
		(provider_id, metric, used, "limit", reset_at, collected_at, raw_json)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, snap := range snapshots {
		// Round collected_at to nearest second for idempotency comparison
		roundedTime := snap.CollectedAt.Truncate(time.Second)

		result, err := stmt.Exec(
			snap.ProviderID, snap.Metric, snap.Used,
			snap.Limit, snap.ResetAt, roundedTime, snap.RawJSON,
		)
		if err != nil {
			return err
		}

		rowsAffected, _ := result.RowsAffected()
		if rowsAffected == 0 {
			// Duplicate was ignored (idempotent)
			continue
		}
	}

	return tx.Commit()
}

// GetLatestUsage retrieves the most recent usage for a provider and metric
func (s *Store) GetLatestUsage(providerID int64, metric string) (*UsageSnapshot, error) {
	snap := &UsageSnapshot{}
	var limitVal sql.NullFloat64
	var resetAt sql.NullTime

	err := s.db.QueryRow(`
		SELECT id, provider_id, metric, used, "limit", reset_at, collected_at, raw_json
		FROM usage_snapshots
		WHERE provider_id = ? AND metric = ?
		ORDER BY collected_at DESC
		LIMIT 1
	`, providerID, metric).Scan(
		&snap.ID, &snap.ProviderID, &snap.Metric, &snap.Used,
		&limitVal, &resetAt, &snap.CollectedAt, &snap.RawJSON,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	if limitVal.Valid {
		snap.Limit = &limitVal.Float64
	}
	if resetAt.Valid {
		snap.ResetAt = &resetAt.Time
	}

	return snap, nil
}

// GetLatestUsageByProvider retrieves all latest metrics for a provider
func (s *Store) GetLatestUsageByProvider(providerID int64) ([]*UsageSnapshot, error) {
	rows, err := s.db.Query(`
		SELECT us.id, us.provider_id, us.metric, us.used, us."limit", us.reset_at, us.collected_at, us.raw_json
		FROM usage_snapshots us
		INNER JOIN (
			SELECT metric, MAX(collected_at) as max_time
			FROM usage_snapshots
			WHERE provider_id = ?
			GROUP BY metric
		) latest ON us.metric = latest.metric AND us.collected_at = latest.max_time
		WHERE us.provider_id = ?
		ORDER BY us.metric
	`, providerID, providerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var snapshots []*UsageSnapshot
	for rows.Next() {
		snap := &UsageSnapshot{}
		var limitVal sql.NullFloat64
		var resetAt sql.NullTime

		err := rows.Scan(
			&snap.ID, &snap.ProviderID, &snap.Metric, &snap.Used,
			&limitVal, &resetAt, &snap.CollectedAt, &snap.RawJSON,
		)
		if err != nil {
			return nil, err
		}

		if limitVal.Valid {
			snap.Limit = &limitVal.Float64
		}
		if resetAt.Valid {
			snap.ResetAt = &resetAt.Time
		}

		snapshots = append(snapshots, snap)
	}

	return snapshots, rows.Err()
}

// GetUsageTrends retrieves usage trends for a time range
// If metric is empty, returns all metrics
func (s *Store) GetUsageTrends(providerID int64, metric string, startTime, endTime time.Time) ([]*UsageSnapshot, error) {
	var query string
	var args []interface{}

	if metric == "" {
		// Return all metrics
		query = `
			SELECT id, provider_id, metric, used, "limit", reset_at, collected_at, raw_json
			FROM usage_snapshots
			WHERE provider_id = ? AND collected_at BETWEEN ? AND ?
			ORDER BY collected_at ASC, metric ASC
		`
		args = []interface{}{providerID, startTime, endTime}
	} else {
		// Return specific metric
		query = `
			SELECT id, provider_id, metric, used, "limit", reset_at, collected_at, raw_json
			FROM usage_snapshots
			WHERE provider_id = ? AND metric = ? AND collected_at BETWEEN ? AND ?
			ORDER BY collected_at ASC
		`
		args = []interface{}{providerID, metric, startTime, endTime}
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var snapshots []*UsageSnapshot
	for rows.Next() {
		snap := &UsageSnapshot{}
		var limitVal sql.NullFloat64
		var resetAt sql.NullTime

		err := rows.Scan(
			&snap.ID, &snap.ProviderID, &snap.Metric, &snap.Used,
			&limitVal, &resetAt, &snap.CollectedAt, &snap.RawJSON,
		)
		if err != nil {
			return nil, err
		}

		if limitVal.Valid {
			snap.Limit = &limitVal.Float64
		}
		if resetAt.Valid {
			snap.ResetAt = &resetAt.Time
		}

		snapshots = append(snapshots, snap)
	}

	return snapshots, rows.Err()
}

// GetAggregatedUsage returns aggregated usage for a time range
func (s *Store) GetAggregatedUsage(providerID int64, metric string, startTime, endTime time.Time) (float64, error) {
	var total sql.NullFloat64
	err := s.db.QueryRow(`
		SELECT SUM(used)
		FROM usage_snapshots
		WHERE provider_id = ? AND metric = ? AND collected_at BETWEEN ? AND ?
	`, providerID, metric, startTime, endTime).Scan(&total)
	if err != nil {
		return 0, err
	}
	if !total.Valid {
		return 0, nil
	}
	return total.Float64, nil
}

// DeleteOldUsage removes usage data older than the specified time
func (s *Store) DeleteOldUsage(olderThan time.Time) (int64, error) {
	result, err := s.db.Exec(`
		DELETE FROM usage_snapshots
		WHERE collected_at < ?
	`, olderThan)
	if err != nil {
		return 0, err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}

	return rows, nil
}
