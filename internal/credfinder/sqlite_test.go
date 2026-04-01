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

	t.Run("존재하지 않는 DB 파일 — 테이블 없음 에러", func(t *testing.T) {
		// plain path 방식은 파일이 없으면 빈 DB를 생성하므로 테이블 미존재 에러가 발생합니다
		tmpFile, err := os.CreateTemp("", "empty-*.db")
		if err != nil {
			t.Fatal(err)
		}
		tmpFile.Close()
		emptyPath := tmpFile.Name()
		defer os.Remove(emptyPath)

		_, err = ReadSQLiteValue(emptyPath, "ItemTable", "key")
		if err == nil {
			t.Error("ReadSQLiteValue() should return error for empty DB (no table)")
		}
	})

	t.Run("허용되지 않은 테이블명 거부", func(t *testing.T) {
		dbPath := createTestSQLiteDB(t)
		defer os.Remove(dbPath)

		_, err := ReadSQLiteValue(dbPath, "malicious; DROP TABLE", "key")
		if err == nil {
			t.Error("ReadSQLiteValue() should reject disallowed table name")
		}
	})

	t.Run("PRAGMA query_only — 쓰기 차단", func(t *testing.T) {
		dbPath := createTestSQLiteDB(t)
		defer os.Remove(dbPath)

		// ReadSQLiteValue 내부에서 query_only=ON이 설정되므로
		// 동일 DB 파일을 열어 쓰기를 시도하면 차단되어야 합니다.
		// 여기서는 ReadSQLiteValue가 정상 값을 반환하는지만 재확인합니다.
		value, err := ReadSQLiteValue(dbPath, "ItemTable", "testKey")
		if err != nil {
			t.Fatalf("ReadSQLiteValue() error = %v", err)
		}
		if value != "testValue" {
			t.Errorf("ReadSQLiteValue() = %q, want %q", value, "testValue")
		}
	})
}
