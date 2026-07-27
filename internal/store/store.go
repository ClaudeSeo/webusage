package store

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Store manages SQLite database operations
type Store struct {
	db *sql.DB
}

// NewStore creates a new database connection with WAL mode
func NewStore(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	// Configure SQLite for concurrent access
	if err := configureDB(db); err != nil {
		_ = db.Close()
		return nil, err
	}

	// Initialize schema
	if err := initSchema(db); err != nil {
		_ = db.Close()
		return nil, err
	}

	if err := normalizeCollectedAtUTC(db); err != nil {
		_ = db.Close()
		return nil, err
	}

	return &Store{db: db}, nil
}

// configureDB sets up SQLite with optimal settings
func configureDB(db *sql.DB) error {
	// Limit connections to match SQLite's single-writer model
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	settings := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA cache_size=10000",
		"PRAGMA temp_store=memory",
		"PRAGMA foreign_keys=ON",
	}

	for _, setting := range settings {
		if _, err := db.Exec(setting); err != nil {
			return fmt.Errorf("setting %s: %w", setting, err)
		}
	}

	return nil
}

// initSchema creates the database schema if it doesn't exist
func initSchema(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS providers (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT UNIQUE NOT NULL,
		enabled BOOLEAN DEFAULT TRUE,
		config_json TEXT,
		last_run DATETIME,
		last_error TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS usage_snapshots (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		provider_id INTEGER NOT NULL,
		metric TEXT NOT NULL,
		used REAL NOT NULL,
		"limit" REAL,
		reset_at DATETIME,
		collected_at DATETIME NOT NULL,
		raw_json TEXT,
		FOREIGN KEY (provider_id) REFERENCES providers(id) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS provider_metric_preferences (
		provider_id INTEGER PRIMARY KEY,
		items_json TEXT NOT NULL,
		version INTEGER NOT NULL CHECK (version >= 1),
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (provider_id) REFERENCES providers(id) ON DELETE CASCADE
	);

	CREATE UNIQUE INDEX IF NOT EXISTS idx_usage_idempotent ON usage_snapshots(provider_id, metric, collected_at);
	CREATE INDEX IF NOT EXISTS idx_provider_collected ON usage_snapshots(provider_id, collected_at);
	CREATE INDEX IF NOT EXISTS idx_metric_collected ON usage_snapshots(metric, collected_at);
	CREATE INDEX IF NOT EXISTS idx_collected ON usage_snapshots(collected_at DESC);

	`

	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("creating schema: %w", err)
	}

	return nil
}

// normalizeCollectedAtUTC repairs rows written before collection timestamps were
// standardized on UTC. Mixed timezone strings do not sort chronologically in SQLite.
func normalizeCollectedAtUTC(db *sql.DB) error {
	rows, err := db.Query(`
		SELECT id, collected_at
		FROM usage_snapshots
		WHERE collected_at NOT LIKE '% +0000 UTC'
	`)
	if err != nil {
		return fmt.Errorf("querying non-UTC collection timestamps: %w", err)
	}

	type timestampUpdate struct {
		id          int64
		collectedAt time.Time
	}
	var updates []timestampUpdate
	for rows.Next() {
		var update timestampUpdate
		if err := rows.Scan(&update.id, &update.collectedAt); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scanning non-UTC collection timestamp: %w", err)
		}
		update.collectedAt = update.collectedAt.UTC()
		updates = append(updates, update)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterating non-UTC collection timestamps: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("closing non-UTC collection timestamp rows: %w", err)
	}
	if len(updates) == 0 {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("starting collection timestamp normalization: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, update := range updates {
		result, err := tx.Exec(`
			UPDATE OR IGNORE usage_snapshots
			SET collected_at = ?
			WHERE id = ?
		`, update.collectedAt, update.id)
		if err != nil {
			return fmt.Errorf("normalizing collection timestamp %d: %w", update.id, err)
		}
		rowsAffected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("checking normalized collection timestamp %d: %w", update.id, err)
		}
		if rowsAffected == 0 {
			if _, err := tx.Exec(`DELETE FROM usage_snapshots WHERE id = ?`, update.id); err != nil {
				return fmt.Errorf("removing duplicate collection timestamp %d: %w", update.id, err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing collection timestamp normalization: %w", err)
	}
	return nil
}

// Close closes the database connection
func (s *Store) Close() error {
	return s.db.Close()
}

// DB returns the underlying database connection
func (s *Store) DB() *sql.DB {
	return s.db
}
