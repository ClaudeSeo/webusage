package kirocli

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"time"

	_ "modernc.org/sqlite" // register sqlite driver (for auth_kv lookup)
)

// authKVKey is the key in the data.sqlite3 auth_kv table where the kiro-cli IdC token is stored.
const authKVKey = "kirocli:odic:token"

// profileStateKey is the key in the data.sqlite3 state table where the profileArn JSON is stored.
const profileStateKey = "api.codewhisperer.profile"

// authkvToken is the snake_case structure of data.sqlite3 auth_kv['kirocli:odic:token'].
// It is the current token kiro-cli refreshes and uses. AccessToken/RefreshToken are sensitive —
// never expose them in logs or errors. The provider does not currently refresh the
// RefreshToken (refresh is delegated to kiro-cli), but we parse it for future refresh paths.
type authkvToken struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	Region       string    `json:"region"`
	StartURL     string    `json:"start_url"`
	OAuthFlow    string    `json:"oauth_flow"`
	Scopes       []string  `json:"scopes"`
}

// defaultDataDBPath returns ~/Library/Application Support/kiro-cli/data.sqlite3.
// Returns an empty string on non-darwin (kiro-cli is macOS only).
func defaultDataDBPath() string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Library", "Application Support", "kiro-cli", "data.sqlite3")
}

// loadTokenFromDB reads and parses the kiro-cli token from auth_kv in data.sqlite3.
// Returns an error if the token is missing or parsing fails. The value itself is never logged.
func loadTokenFromDB(dbPath string) (*authkvToken, error) {
	if dbPath == "" {
		return nil, errors.New("kirocli: data.sqlite3 path is empty")
	}
	dsn := "file:" + dbPath + "?mode=ro&_pragma=busy_timeout(5000)"
	db, err := openReadonlyDB(dsn)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	var value []byte
	err = db.QueryRow(`SELECT value FROM auth_kv WHERE key = ?`, authKVKey).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("kirocli: kiro-cli token not found in auth_kv")
	}
	if err != nil {
		return nil, fmt.Errorf("kirocli: reading auth_kv: %w", err)
	}
	var tok authkvToken
	if err := json.Unmarshal(value, &tok); err != nil {
		return nil, fmt.Errorf("kirocli: parsing auth_kv token: %w", err)
	}
	return &tok, nil
}

// resolveProfileArn extracts the profileArn (the arn field) from the state key in data.sqlite3.
func resolveProfileArn(dbPath string) (string, error) {
	if dbPath == "" {
		return "", errors.New("kirocli: data.sqlite3 path is empty")
	}
	dsn := "file:" + dbPath + "?mode=ro&_pragma=busy_timeout(5000)"
	db, err := openReadonlyDB(dsn)
	if err != nil {
		return "", err
	}
	defer func() { _ = db.Close() }()

	var value []byte
	err = db.QueryRow(`SELECT value FROM state WHERE key = ?`, profileStateKey).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errors.New("kirocli: profile not found in state")
	}
	if err != nil {
		return "", fmt.Errorf("kirocli: reading profileArn: %w", err)
	}
	var prof struct {
		Arn         string `json:"arn"`
		ProfileName string `json:"profile_name"`
	}
	if err := json.Unmarshal(value, &prof); err != nil {
		return "", fmt.Errorf("kirocli: parsing profileArn: %w", err)
	}
	if prof.Arn == "" {
		return "", errors.New("kirocli: profileArn is empty")
	}
	return prof.Arn, nil
}

// regionRe validates the AWS region format (lowercase alphanumeric plus hyphens). Since the
// region is placed into the URL host, this prevents out-of-format values (e.g. "evil.com/path#")
// from leading to SSRF. The region source is a local trusted DB so exploitability is low,
// but this is defense in depth.
var regionRe = regexp.MustCompile(`^[a-z0-9-]+$`)

// regionFromProfileArn extracts the control plane region from the profileArn
// (arn:aws:codewhisperer:<region>:...). The Kiro control plane is keyed by the profileArn
// region (e.g. us-east-1), which may differ from the token's IDC region (e.g. ap-northeast-2) —
// some regions have no management.<idc-region>.kiro.dev host in DNS.
// Returns "us-east-1" (the kiro-cli default) on format validation failure, empty value, or parse failure.
func regionFromProfileArn(arn string) string {
	// arn:aws:codewhisperer:<region>:<account>:profile/<id>
	parts := splitARN(arn)
	if len(parts) >= 4 && regionRe.MatchString(parts[3]) {
		return parts[3]
	}
	return "us-east-1"
}

// splitARN splits an ARN on ':'. arn:aws:service:region:account:resource.
func splitARN(arn string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(arn); i++ {
		if arn[i] == ':' {
			parts = append(parts, arn[start:i])
			start = i + 1
		}
	}
	parts = append(parts, arn[start:])
	return parts
}

// tokenExpired returns true if the access token has expired. We do not add a 3-minute
// skew and check only the expiry — kiro-cli handles refresh, so we never use an expired token.
func tokenExpired(t *authkvToken, now time.Time) bool {
	if t == nil {
		return true
	}
	return !t.ExpiresAt.After(now)
}
