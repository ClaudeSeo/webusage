package store

import (
	"database/sql"
	"encoding/json"
	"time"
)

// Provider represents a configured AI provider
type Provider struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Enabled    bool       `json:"enabled"`
	ConfigJSON string     `json:"config_json,omitempty"`
	LastRun    *time.Time `json:"last_run,omitempty"`
	LastError  *string    `json:"last_error,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// CreateProvider inserts a new provider
func (s *Store) CreateProvider(name, configJSON string) (int64, error) {
	result, err := s.db.Exec(`
		INSERT INTO providers (name, enabled, config_json)
		VALUES (?, TRUE, ?)
	`, name, configJSON)
	if err != nil {
		return 0, err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}

	return id, nil
}

// CreateProviderDisabled는 provider를 비활성화 상태로 등록합니다 (INSERT OR IGNORE)
// 이미 존재하는 provider는 무시합니다
func (s *Store) CreateProviderDisabled(name, displayName, configJSON string) (int64, error) {
	result, err := s.db.Exec(`
		INSERT OR IGNORE INTO providers (name, enabled, config_json)
		VALUES (?, FALSE, ?)
	`, name, configJSON)
	if err != nil {
		return 0, err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}

	// INSERT OR IGNORE가 무시된 경우 기존 ID를 반환
	if id == 0 {
		existing, err := s.GetProviderByName(name)
		if err != nil {
			return 0, err
		}
		return existing.ID, nil
	}

	return id, nil
}

// GetProvider retrieves a provider by ID
func (s *Store) GetProvider(id int64) (*Provider, error) {
	p := &Provider{}
	var lastRun sql.NullTime
	var lastError sql.NullString

	err := s.db.QueryRow(`
		SELECT id, name, enabled, config_json, last_run, last_error, created_at, updated_at
		FROM providers
		WHERE id = ?
	`, id).Scan(
		&p.ID, &p.Name, &p.Enabled, &p.ConfigJSON,
		&lastRun, &lastError, &p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	if lastRun.Valid {
		p.LastRun = &lastRun.Time
	}
	if lastError.Valid {
		p.LastError = &lastError.String
	}

	return p, nil
}

// GetProviderByName retrieves a provider by name
func (s *Store) GetProviderByName(name string) (*Provider, error) {
	p := &Provider{}
	var lastRun sql.NullTime
	var lastError sql.NullString

	err := s.db.QueryRow(`
		SELECT id, name, enabled, config_json, last_run, last_error, created_at, updated_at
		FROM providers
		WHERE name = ?
	`, name).Scan(
		&p.ID, &p.Name, &p.Enabled, &p.ConfigJSON,
		&lastRun, &lastError, &p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	if lastRun.Valid {
		p.LastRun = &lastRun.Time
	}
	if lastError.Valid {
		p.LastError = &lastError.String
	}

	return p, nil
}

// ListProviders returns all providers
func (s *Store) ListProviders() ([]*Provider, error) {
	rows, err := s.db.Query(`
		SELECT id, name, enabled, config_json, last_run, last_error, created_at, updated_at
		FROM providers
		ORDER BY name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var providers []*Provider
	for rows.Next() {
		p := &Provider{}
		var lastRun sql.NullTime
		var lastError sql.NullString

		err := rows.Scan(
			&p.ID, &p.Name, &p.Enabled, &p.ConfigJSON,
			&lastRun, &lastError, &p.CreatedAt, &p.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}

		if lastRun.Valid {
			p.LastRun = &lastRun.Time
		}
		if lastError.Valid {
			p.LastError = &lastError.String
		}

		providers = append(providers, p)
	}

	return providers, rows.Err()
}

// UpdateProviderStatus updates the last run time and error status
func (s *Store) UpdateProviderStatus(id int64, lastError *string) error {
	now := time.Now()
	_, err := s.db.Exec(`
		UPDATE providers
		SET last_run = ?, last_error = ?, updated_at = ?
		WHERE id = ?
	`, now, lastError, now, id)
	return err
}

// EnableProvider enables or disables a provider by ID
func (s *Store) EnableProvider(id int64, enabled bool) error {
	_, err := s.db.Exec(`
		UPDATE providers SET enabled = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, enabled, id)
	return err
}

// EnableProviderByName enables or disables a provider by name
func (s *Store) EnableProviderByName(name string, enabled bool) error {
	_, err := s.db.Exec(`
		UPDATE providers SET enabled = ?, updated_at = CURRENT_TIMESTAMP
		WHERE name = ?
	`, enabled, name)
	return err
}

// DeleteProvider removes a provider
func (s *Store) DeleteProvider(id int64) error {
	_, err := s.db.Exec(`DELETE FROM providers WHERE id = ?`, id)
	return err
}

// DeleteProviderByName removes a provider and its usage data by name
func (s *Store) DeleteProviderByName(name string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		DELETE FROM usage_snapshots WHERE provider_id IN (SELECT id FROM providers WHERE name = ?)
	`, name)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`DELETE FROM providers WHERE name = ?`, name)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// ProviderConfig helpers

// ProviderConfigDB는 provider 인증 방식과 자격증명 출처를 저장합니다.
// API key는 저장하지 않으며 OAuth credential 경로와 방식만 기록합니다.
type ProviderConfigDB struct {
	AuthMethod string `json:"auth_method"`
	CredSource string `json:"cred_source,omitempty"` // 자격증명 발견 경로 (파일경로, keychain 등)
	BaseURL    string `json:"base_url,omitempty"`
}

func MarshalProviderConfig(config ProviderConfigDB) (string, error) {
	b, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func UnmarshalProviderConfig(jsonStr string) (*ProviderConfigDB, error) {
	var config ProviderConfigDB
	if err := json.Unmarshal([]byte(jsonStr), &config); err != nil {
		return nil, err
	}
	return &config, nil
}
