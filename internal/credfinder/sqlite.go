package credfinder

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // sqlite 드라이버 등록
)

// allowedTables — SQL injection 방지를 위한 테이블명 allowlist
var allowedTables = map[string]bool{
	"ItemTable": true, // Cursor state.vscdb
}

// ReadSQLiteValue는 SQLite DB에서 key-value 테이블의 값을 읽습니다.
// 외부 앱 DB를 건드리므로 PRAGMA query_only=ON으로 쓰기를 차단합니다.
// Note: modernc.org/sqlite는 URI mode=ro를 완전히 지원하지 않으므로
// 파일 경로 직접 열기 + PRAGMA query_only 방식을 사용합니다.
func ReadSQLiteValue(dbPath, tableName, key string) (string, error) {
	if !allowedTables[tableName] {
		return "", fmt.Errorf("disallowed table name: %q", tableName)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return "", fmt.Errorf("cannot open database at %q — check the file exists and is readable: %w", dbPath, err)
	}
	defer db.Close()

	// 읽기전용 강제: 외부 앱 DB 손상 방지
	if _, err := db.Exec("PRAGMA query_only=ON"); err != nil {
		return "", fmt.Errorf("setting query_only pragma on %q: %w", dbPath, err)
	}

	query := fmt.Sprintf("SELECT value FROM %s WHERE key = ?", tableName) //nolint:gosec
	var value string
	err = db.QueryRow(query, key).Scan(&value)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("querying %s in %q for key=%q: %w", tableName, dbPath, key, err)
	}
	return value, nil
}
