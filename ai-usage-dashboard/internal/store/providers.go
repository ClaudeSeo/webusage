package store

import (
	"database/sql"
	"encoding/json"
	"time"
)

// Provider represents a configured AI provider
type Provider struct {
	ID        int64      `json:"id"`
	Name      string     `json:"name"`
	Enabled   bool       `json:"enabled"`
	ConfigJSON string    `json:"config_json,omitempty"`
	LastRun   *time.Time `json:"last_run,omitempty"`
	LastError *string    `json:"last_error,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
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

// EnableProvider enables or disables a provider
func (s *Store) EnableProvider(id int64, enabled bool) error {
	_, err := s.db.Exec(`
		UPDATE providers SET enabled = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, enabled, id)
	return err
}

// DeleteProvider removes a provider
func (s *Store) DeleteProvider(id int64) error {
	_, err := s.db.Exec(`DELETE FROM providers WHERE id = ?`, id)
	return err
}

// ProviderConfig helpers

type ProviderConfigDB struct {
	APIKey    string `json:"api_key"`
	BaseURL   string `json:"base_url,omitempty"`
	OrgID     string `json:"org_id,omitempty"`
	ProjectID string `json:"project_id,omitempty"`
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
