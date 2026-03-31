package credfinder

import (
	"database/sql"
	"fmt"
	"os"
	"testing"

	_ "modernc.org/sqlite"
)

// createTestSQLiteDB는 테스트용 SQLite DB를 임시 파일로 생성합니다
func createTestSQLiteDB(t *testing.T) string {
	t.Helper()
	tmpFile, err := os.CreateTemp("", "test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()

	// 쓰기 모드로 DB 생성 후 데이터 삽입
	dsn := fmt.Sprintf("file:%s?mode=rwc", tmpFile.Name())
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		os.Remove(tmpFile.Name())
		t.Fatal(err)
	}
	defer db.Close()

	_, err = db.Exec(`CREATE TABLE ItemTable (key TEXT PRIMARY KEY, value TEXT)`)
	if err != nil {
		os.Remove(tmpFile.Name())
		t.Fatalf("creating test table: %v", err)
	}

	_, err = db.Exec(`INSERT INTO ItemTable (key, value) VALUES (?, ?)`, "testKey", "testValue")
	if err != nil {
		os.Remove(tmpFile.Name())
		t.Fatalf("inserting test data: %v", err)
	}

	return tmpFile.Name()
}

func TestReadSQLiteValue(t *testing.T) {
	t.Run("존재하는 키 읽기", func(t *testing.T) {
		dbPath := createTestSQLiteDB(t)
		defer os.Remove(dbPath)

		value, err := ReadSQLiteValue(dbPath, "ItemTable", "testKey")
		if err != nil {
			t.Fatalf("ReadSQLiteValue() error = %v", err)
		}
		if value != "testValue" {
			t.Errorf("ReadSQLiteValue() = %q, want %q", value, "testValue")
		}
	})

	t.Run("존재하지 않는 키", func(t *testing.T) {
		dbPath := createTestSQLiteDB(t)
		defer os.Remove(dbPath)

		_, err := ReadSQLiteValue(dbPath, "ItemTable", "nonexistentKey")
		if err != ErrNotFound {
			t.Errorf("ReadSQLiteValue() error = %v, want ErrNotFound", err)
		}
	})

	t.Run("존재하지 않는 DB 파일", func(t *testing.T) {
		_, err := ReadSQLiteValue("/tmp/nonexistent-test.db", "ItemTable", "key")
		if err == nil {
			t.Error("ReadSQLiteValue() should return error for nonexistent DB")
		}
	})
}
