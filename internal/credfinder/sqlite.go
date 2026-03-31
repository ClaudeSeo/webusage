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
// 외부 앱 DB를 건드리므로 ?mode=ro 읽기전용으로만 열어야 합니다.
func ReadSQLiteValue(dbPath, tableName, key string) (string, error) {
	if !allowedTables[tableName] {
		return "", fmt.Errorf("disallowed table name: %q", tableName)
	}

	// 읽기전용 URI 모드로 열기: 외부 DB 손상 방지
	dsn := fmt.Sprintf("file:%s?mode=ro", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return "", fmt.Errorf("opening sqlite db %q: %w", dbPath, err)
	}
	defer db.Close()

	query := fmt.Sprintf("SELECT value FROM %s WHERE key = ?", tableName) //nolint:gosec
	var value string
	err = db.QueryRow(query, key).Scan(&value)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("querying %s.%s key=%q: %w", tableName, dbPath, key, err)
	}
	return value, nil
}
