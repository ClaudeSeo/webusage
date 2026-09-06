package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func providerNames(providers []*Provider) []string {
	names := make([]string, 0, len(providers))
	for _, provider := range providers {
		names = append(names, provider.Name)
	}
	return names
}

func assertProviderOrder(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("provider order = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("provider order = %v, want %v", got, want)
		}
	}
}

func mustCreateStoreProviders(t *testing.T, testStore *Store, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, err := testStore.CreateProvider(name, `{"auth_method":"api_key"}`); err != nil {
			t.Fatalf("CreateProvider(%q) error = %v", name, err)
		}
	}
}

func TestListProvidersShouldReturnAlphabeticalOrderWithoutDisplayOrder(t *testing.T) {
	// Given: providers registered in an order that differs from their names.
	testStore, cleanup := setupTestStore(t)
	defer cleanup()
	mustCreateStoreProviders(t, testStore, "zeta", "alpha", "mid")

	// When: listing providers before any display order is saved.
	providers, err := testStore.ListProviders()
	if err != nil {
		t.Fatalf("ListProviders() error = %v", err)
	}

	// Then: the names come back alphabetically.
	assertProviderOrder(t, providerNames(providers), []string{"alpha", "mid", "zeta"})
}

func TestSaveProviderOrderShouldPersistSavedOrder(t *testing.T) {
	// Given: providers with the alphabetical default and known config.
	testStore, cleanup := setupTestStore(t)
	defer cleanup()
	mustCreateStoreProviders(t, testStore, "kiro", "ollama", "claude")

	// When: saving a new provider display order.
	if err := testStore.SaveProviderOrder([]string{"ollama", "claude", "kiro"}); err != nil {
		t.Fatalf("SaveProviderOrder() error = %v", err)
	}

	// Then: ListProviders returns the saved order.
	providers, err := testStore.ListProviders()
	if err != nil {
		t.Fatalf("ListProviders() error = %v", err)
	}
	assertProviderOrder(t, providerNames(providers), []string{"ollama", "claude", "kiro"})

	// And: identity, enabled state, and config survive the reorder.
	for _, name := range []string{"ollama", "claude", "kiro"} {
		provider, err := testStore.GetProviderByName(name)
		if err != nil {
			t.Fatalf("GetProviderByName(%q) error = %v", name, err)
		}
		if provider == nil {
			t.Fatalf("GetProviderByName(%q) = nil", name)
		}
		if !provider.Enabled {
			t.Errorf("provider %q enabled = false, want true", name)
		}
		if provider.ConfigJSON != `{"auth_method":"api_key"}` {
			t.Errorf("provider %q config = %q, want unchanged", name, provider.ConfigJSON)
		}
	}
}

func TestSaveProviderOrderShouldSortUnorderedProvidersAfterOrderedOnes(t *testing.T) {
	// Given: a saved order that covers only part of the providers.
	testStore, cleanup := setupTestStore(t)
	defer cleanup()
	mustCreateStoreProviders(t, testStore, "zeta", "alpha")
	if err := testStore.SaveProviderOrder([]string{"zeta"}); err != nil {
		t.Fatalf("SaveProviderOrder() error = %v", err)
	}
	mustCreateStoreProviders(t, testStore, "mid", "beta")

	// When: listing providers afterwards.
	providers, err := testStore.ListProviders()
	if err != nil {
		t.Fatalf("ListProviders() error = %v", err)
	}

	// Then: ordered providers come first and never-ordered ones follow by name.
	assertProviderOrder(t, providerNames(providers), []string{"zeta", "alpha", "beta", "mid"})
}

func TestNewStoreShouldAddDisplayOrderToLegacyDatabase(t *testing.T) {
	// Given: a database whose providers table predates the display_order column.
	dbPath := filepath.Join(t.TempDir(), "usage.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	legacySchema := `
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
	`
	if _, err := db.Exec(legacySchema); err != nil {
		t.Fatalf("creating legacy schema error = %v", err)
	}
	for _, name := range []string{"zeta", "alpha"} {
		if _, err := db.Exec(`INSERT INTO providers (name, config_json) VALUES (?, ?)`, name, `{"auth_method":"api_key"}`); err != nil {
			t.Fatalf("inserting legacy provider %q error = %v", name, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("closing legacy database error = %v", err)
	}

	// When: the database is opened through the store.
	testStore, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer func() {
		if err := testStore.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()

	// Then: pre-existing data keeps alphabetical order and reorder works.
	providers, err := testStore.ListProviders()
	if err != nil {
		t.Fatalf("ListProviders() error = %v", err)
	}
	assertProviderOrder(t, providerNames(providers), []string{"alpha", "zeta"})

	if err := testStore.SaveProviderOrder([]string{"zeta", "alpha"}); err != nil {
		t.Fatalf("SaveProviderOrder() error = %v", err)
	}
	providers, err = testStore.ListProviders()
	if err != nil {
		t.Fatalf("ListProviders() after reorder error = %v", err)
	}
	assertProviderOrder(t, providerNames(providers), []string{"zeta", "alpha"})
}
