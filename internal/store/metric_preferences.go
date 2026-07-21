package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ClaudeSeo/webusage/internal/domain"
)

// ErrMetricPreferenceVersionConflict는 저장된 version과 expected version이 다를 때 반환된다.
var ErrMetricPreferenceVersionConflict = errors.New("metric preference version conflict")

// MetricPreference는 Provider별로 저장된 metric 노출 설정과 version이다.
type MetricPreference struct {
	ProviderID int64
	Version    int64
	Items      []domain.MetricPreferenceItem
}

// MetricPreferenceUpdate는 CAS 저장에 필요한 Provider별 변경이다.
type MetricPreferenceUpdate struct {
	ProviderID      int64
	ExpectedVersion int64
	Items           []domain.MetricPreferenceItem
}

// GetMetricPreference는 저장된 설정을 조회하며 행이 없으면 version 0을 반환한다.
func (s *Store) GetMetricPreference(providerID int64) (*MetricPreference, error) {
	preference := &MetricPreference{
		ProviderID: providerID,
		Items:      []domain.MetricPreferenceItem{},
	}
	var itemsJSON string
	err := s.db.QueryRow(`
		SELECT items_json, version
		FROM provider_metric_preferences
		WHERE provider_id = ?
	`, providerID).Scan(&itemsJSON, &preference.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return preference, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting metric preference for provider %d: %w", providerID, err)
	}

	if err := json.Unmarshal([]byte(itemsJSON), &preference.Items); err != nil {
		return nil, fmt.Errorf("decoding metric preference for provider %d: %w", providerID, err)
	}
	if preference.Items == nil {
		preference.Items = []domain.MetricPreferenceItem{}
	}

	return preference, nil
}

// SaveMetricPreferences는 모든 Provider 변경을 하나의 CAS transaction으로 저장한다.
func (s *Store) SaveMetricPreferences(updates []MetricPreferenceUpdate) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning metric preference transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, update := range updates {
		items := update.Items
		if items == nil {
			items = []domain.MetricPreferenceItem{}
		}
		itemsJSON, err := json.Marshal(items)
		if err != nil {
			return fmt.Errorf("encoding metric preference for provider %d: %w", update.ProviderID, err)
		}

		var result sql.Result
		if update.ExpectedVersion == 0 {
			result, err = tx.Exec(`
				INSERT INTO provider_metric_preferences (provider_id, items_json, version)
				VALUES (?, ?, 1)
				ON CONFLICT(provider_id) DO NOTHING
			`, update.ProviderID, string(itemsJSON))
		} else {
			result, err = tx.Exec(`
				UPDATE provider_metric_preferences
				SET items_json = ?, version = version + 1, updated_at = CURRENT_TIMESTAMP
				WHERE provider_id = ? AND version = ?
			`, string(itemsJSON), update.ProviderID, update.ExpectedVersion)
		}
		if err != nil {
			return fmt.Errorf("saving metric preference for provider %d: %w", update.ProviderID, err)
		}

		rowsAffected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("checking metric preference update for provider %d: %w", update.ProviderID, err)
		}
		if rowsAffected != 1 {
			return fmt.Errorf("%w: provider %d", ErrMetricPreferenceVersionConflict, update.ProviderID)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing metric preferences: %w", err)
	}
	return nil
}
