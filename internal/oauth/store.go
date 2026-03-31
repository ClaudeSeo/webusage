package oauth

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// CredentialStore는 OAuth 자격증명의 저장/조회/삭제 인터페이스
type CredentialStore interface {
	// Get은 provider 이름으로 토큰을 조회합니다
	Get(ctx context.Context, providerName string) (*Token, error)
	// Save는 토큰을 저장합니다 (이미 존재하면 덮어씁니다)
	Save(ctx context.Context, providerName string, token *Token) error
	// Delete는 provider의 저장된 자격증명을 삭제합니다
	Delete(ctx context.Context, providerName string) error
}

// FileCredentialStore는 JSON 파일 기반 CredentialStore 구현체
type FileCredentialStore struct {
	filePath string
}

// fileCredentials는 파일에 저장되는 자격증명 맵 형태
type fileCredentials map[string]*Token

// NewFileCredentialStore는 지정된 경로의 JSON 파일을 사용하는 store를 생성합니다
func NewFileCredentialStore(filePath string) *FileCredentialStore {
	return &FileCredentialStore{filePath: filePath}
}

// Get은 파일에서 provider의 토큰을 읽습니다
func (s *FileCredentialStore) Get(_ context.Context, providerName string) (*Token, error) {
	creds, err := s.load()
	if err != nil {
		return nil, err
	}
	token, ok := creds[providerName]
	if !ok {
		return nil, nil
	}
	return token, nil
}

// Save는 파일에 provider 토큰을 저장합니다
func (s *FileCredentialStore) Save(_ context.Context, providerName string, token *Token) error {
	creds, err := s.load()
	if err != nil {
		// 파일이 없으면 새로 생성
		creds = make(fileCredentials)
	}
	creds[providerName] = token
	return s.persist(creds)
}

// Delete는 파일에서 provider 토큰을 삭제합니다
func (s *FileCredentialStore) Delete(_ context.Context, providerName string) error {
	creds, err := s.load()
	if err != nil {
		return err
	}
	delete(creds, providerName)
	return s.persist(creds)
}

func (s *FileCredentialStore) load() (fileCredentials, error) {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return make(fileCredentials), nil
		}
		return nil, fmt.Errorf("reading credential file: %w", err)
	}
	var creds fileCredentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("parsing credential file: %w", err)
	}
	return creds, nil
}

func (s *FileCredentialStore) persist(creds fileCredentials) error {
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return fmt.Errorf("serializing credentials: %w", err)
	}
	// 0600: 소유자만 읽기/쓰기 가능
	if err := os.WriteFile(s.filePath, data, 0600); err != nil {
		return fmt.Errorf("writing credential file: %w", err)
	}
	return nil
}

// DBCredentialStore는 SQLite 기반 CredentialStore 구현체
// Task #10에서 modernc.org/sqlite 전환 후 사용 가능
type DBCredentialStore struct {
	db *sql.DB
}

// NewDBCredentialStore는 SQLite DB를 사용하는 store를 생성합니다
func NewDBCredentialStore(db *sql.DB) *DBCredentialStore {
	return &DBCredentialStore{db: db}
}

// Get은 DB에서 provider의 토큰을 조회합니다
func (s *DBCredentialStore) Get(ctx context.Context, providerName string) (*Token, error) {
	const query = `
		SELECT access_token, refresh_token, token_type, expires_at, scopes
		FROM oauth_credentials
		WHERE provider_name = ?
	`
	row := s.db.QueryRowContext(ctx, query, providerName)

	var (
		accessToken  string
		refreshToken sql.NullString
		tokenType    sql.NullString
		expiresAt    sql.NullTime
		scopesStr    sql.NullString
	)

	err := row.Scan(&accessToken, &refreshToken, &tokenType, &expiresAt, &scopesStr)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying oauth_credentials: %w", err)
	}

	token := &Token{
		AccessToken: accessToken,
	}
	if refreshToken.Valid {
		token.RefreshToken = refreshToken.String
	}
	if tokenType.Valid {
		token.TokenType = tokenType.String
	}
	if expiresAt.Valid {
		exp := expiresAt.Time
		token.ExpiresAt = &exp
	}
	if scopesStr.Valid && scopesStr.String != "" {
		token.Scopes = strings.Split(scopesStr.String, " ")
	}

	return token, nil
}

// Save는 DB에 provider 토큰을 저장합니다 (upsert)
func (s *DBCredentialStore) Save(ctx context.Context, providerName string, token *Token) error {
	const query = `
		INSERT INTO oauth_credentials (provider_name, access_token, refresh_token, token_type, expires_at, scopes, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(provider_name) DO UPDATE SET
			access_token = excluded.access_token,
			refresh_token = excluded.refresh_token,
			token_type = excluded.token_type,
			expires_at = excluded.expires_at,
			scopes = excluded.scopes,
			updated_at = CURRENT_TIMESTAMP
	`

	var expiresAt *time.Time
	if token.ExpiresAt != nil {
		expiresAt = token.ExpiresAt
	}

	scopesStr := strings.Join(token.Scopes, " ")

	_, err := s.db.ExecContext(ctx, query,
		providerName,
		token.AccessToken,
		nullString(token.RefreshToken),
		nullString(token.TokenType),
		expiresAt,
		nullString(scopesStr),
	)
	if err != nil {
		return fmt.Errorf("saving oauth_credentials: %w", err)
	}
	return nil
}

// Delete는 DB에서 provider 자격증명을 삭제합니다
func (s *DBCredentialStore) Delete(ctx context.Context, providerName string) error {
	const query = `DELETE FROM oauth_credentials WHERE provider_name = ?`
	_, err := s.db.ExecContext(ctx, query, providerName)
	if err != nil {
		return fmt.Errorf("deleting oauth_credentials: %w", err)
	}
	return nil
}

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
