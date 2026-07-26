package kirocli

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// httpDoer is the system boundary seam for HTTP calls. Tests inject a stub based on httptest.Server.
type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// httpDefaultDoer wraps the default http.Client. As with the openusage client,
// a 10s timeout ensures a Kiro API hang does not indefinitely block the collector cycle/shutdown.
var defaultHTTPClient = &http.Client{Timeout: 10 * time.Second}

type httpDefaultDoer struct{}

func (httpDefaultDoer) Do(req *http.Request) (*http.Response, error) {
	return defaultHTTPClient.Do(req)
}

// userAgent is the header value identifying the webusage version.
const userAgent = "webusage/1.0.0"

// openReadonlyDB opens a read-only SQLite connection. Used to read profileArn
// from data.sqlite3.
func openReadonlyDB(dsn string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// Unify the connection pool (same single-writer model convention as store.go).
	db.SetMaxOpenConns(1)
	return db, nil
}

// buildUsageLimitsRequest builds the Get-Usage-Limits request object.
// method=GET, host=management.<region>.kiro.dev, path=/Get-Usage-Limits,
// query: origin=KIRO_CLI, profileArn=<arn URI-encoded>,
// headers: Authorization: Bearer <accessToken>, TokenType: SSO_OIDC, User-Agent.
func buildUsageLimitsRequest(region, accessToken, profileArn string) (*http.Request, error) {
	q := url.Values{}
	q.Set("origin", "KIRO_CLI")
	q.Set("profileArn", profileArn)

	u := &url.URL{
		Scheme:   "https",
		Host:     "management." + region + ".kiro.dev",
		Path:     "/Get-Usage-Limits",
		RawQuery: q.Encode(),
	}

	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("TokenType", "SSO_OIDC")
	req.Header.Set("User-Agent", userAgent)
	return req, nil
}

// getUsageLimits calls Get-Usage-Limits and decodes the response.
// The token is only placed in the Authorization header and is never exposed in error messages.
func getUsageLimits(ctx context.Context, c httpDoer, region, accessToken, profileArn string) (*usageLimitsResponse, error) {
	req, err := buildUsageLimitsRequest(region, accessToken, profileArn)
	if err != nil {
		return nil, fmt.Errorf("kirocli: building request: %w", err)
	}
	req = req.WithContext(ctx)

	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("kirocli: calling Get-Usage-Limits: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("kirocli: reading response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kirocli: Get-Usage-Limits returned status %d", resp.StatusCode)
	}

	var out usageLimitsResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("kirocli: decoding response: %w", err)
	}
	return &out, nil
}
