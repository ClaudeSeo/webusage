package store

import (
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"
)

// Store manages SQLite database operations
type Store struct {
	db *sql.DB
}

// NewStore creates a new database connection with WAL mode
func NewStore(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	
	// Configure SQLite for concurrent access
	if err := configureDB(db); err != nil {
		db.Close()
		return nil, err
	}
	
	// Initialize schema
	if err := initSchema(db); err != nil {
		db.Close()
		return nil, err
	}
	
	return &Store{db: db}, nil
}

// configureDB sets up SQLite with optimal settings
func configureDB(db *sql.DB) error {
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
	
	CREATE INDEX IF NOT EXISTS idx_provider_collected ON usage_snapshots(provider_id, collected_at);
	CREATE INDEX IF NOT EXISTS idx_metric_collected ON usage_snapshots(metric, collected_at);
	CREATE INDEX IF NOT EXISTS idx_collected ON usage_snapshots(collected_at DESC);
	`
	
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("creating schema: %w", err)
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
