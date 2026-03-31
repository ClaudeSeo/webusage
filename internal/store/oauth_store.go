package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// OAuthCredential은 oauth_credentials 테이블의 레코드
type OAuthCredential struct {
	ID           int64
	ProviderName string
	AccessToken  string
	RefreshToken string
	TokenType    string
	ExpiresAt    *time.Time
	Scopes       []string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// GetCredential은 provider 이름으로 OAuth 자격증명을 조회합니다
// 존재하지 않으면 nil, nil 반환
func (s *Store) GetCredential(ctx context.Context, providerName string) (*OAuthCredential, error) {
	const query = `
		SELECT id, provider_name, access_token, refresh_token, token_type,
		       expires_at, scopes, created_at, updated_at
		FROM oauth_credentials
		WHERE provider_name = ?
	`
	row := s.db.QueryRowContext(ctx, query, providerName)
	return scanCredential(row)
}

// SaveCredential은 OAuth 자격증명을 저장합니다 (upsert)
// provider_name이 이미 존재하면 업데이트합니다
func (s *Store) SaveCredential(ctx context.Context, cred *OAuthCredential) error {
	const query = `
		INSERT INTO oauth_credentials (provider_name, access_token, refresh_token, token_type, expires_at, scopes, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(provider_name) DO UPDATE SET
			access_token  = excluded.access_token,
			refresh_token = excluded.refresh_token,
			token_type    = excluded.token_type,
			expires_at    = excluded.expires_at,
			scopes        = excluded.scopes,
			updated_at    = CURRENT_TIMESTAMP
	`

	scopesStr := strings.Join(cred.Scopes, " ")

	_, err := s.db.ExecContext(ctx, query,
		cred.ProviderName,
		cred.AccessToken,
		nullString(cred.RefreshToken),
		nullStringDefault(cred.TokenType, "Bearer"),
		cred.ExpiresAt,
		nullString(scopesStr),
	)
	if err != nil {
		return fmt.Errorf("saving oauth credential: %w", err)
	}
	return nil
}

// DeleteCredential은 provider의 OAuth 자격증명을 삭제합니다
func (s *Store) DeleteCredential(ctx context.Context, providerName string) error {
	const query = `DELETE FROM oauth_credentials WHERE provider_name = ?`
	_, err := s.db.ExecContext(ctx, query, providerName)
	if err != nil {
		return fmt.Errorf("deleting oauth credential: %w", err)
	}
	return nil
}

// scanCredential은 sql.Row에서 OAuthCredential을 스캔합니다
func scanCredential(row *sql.Row) (*OAuthCredential, error) {
	var (
		id           int64
		providerName string
		accessToken  string
		refreshToken sql.NullString
		tokenType    sql.NullString
		expiresAt    sql.NullTime
		scopesStr    sql.NullString
		createdAt    time.Time
		updatedAt    time.Time
	)

	err := row.Scan(&id, &providerName, &accessToken, &refreshToken, &tokenType,
		&expiresAt, &scopesStr, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scanning oauth credential: %w", err)
	}

	cred := &OAuthCredential{
		ID:           id,
		ProviderName: providerName,
		AccessToken:  accessToken,
		TokenType:    "Bearer",
		CreatedAt:    createdAt,
		UpdatedAt:    updatedAt,
	}
	if refreshToken.Valid {
		cred.RefreshToken = refreshToken.String
	}
	if tokenType.Valid && tokenType.String != "" {
		cred.TokenType = tokenType.String
	}
	if expiresAt.Valid {
		exp := expiresAt.Time
		cred.ExpiresAt = &exp
	}
	if scopesStr.Valid && scopesStr.String != "" {
		cred.Scopes = strings.Split(scopesStr.String, " ")
	}

	return cred, nil
}

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func nullStringDefault(s, defaultVal string) sql.NullString {
	if s == "" {
		s = defaultVal
	}
	return sql.NullString{String: s, Valid: true}
}
