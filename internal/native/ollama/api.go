package ollama

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// balanceEndpoint and usageEndpoint are the Ollama Cloud account endpoints.
// They are fixed rather than configurable: the API key is account-scoped to
// ollama.com, so a redirectable host would only widen where that credential
// can be sent. usageEndpoint pins range=30d because the API default (7d) would
// silently shrink the spend window that the monthly cost metric represents.
const (
	balanceEndpoint = "https://ollama.com/api/balance"
	usageEndpoint   = "https://ollama.com/api/usage?range=30d"
)

// userAgent is the header value identifying the webusage version.
const userAgent = "webusage/1.0.0"

// ErrUnauthorized indicates the API key was rejected (401) or the account is
// suspended (403). It is distinct from a transport failure so the operator
// knows to fix OLLAMA_API_KEY or the account rather than wait for the next
// collection cycle.
var ErrUnauthorized = errors.New("ollama: API key rejected or account suspended; check OLLAMA_API_KEY")

// httpDoer is the system boundary seam for HTTP calls. Tests inject a stub based on httptest.Server.
type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// defaultHTTPClient bounds every call with a 10s timeout so an Ollama API hang
// cannot stall the collection cycle, matching the openusage and kirocli clients.
var defaultHTTPClient = &http.Client{Timeout: 10 * time.Second}

type httpDefaultDoer struct{}

func (httpDefaultDoer) Do(req *http.Request) (*http.Response, error) {
	return defaultHTTPClient.Do(req)
}

// buildRequest builds a GET request for one of the fixed endpoints.
// The API key is sent only as a bearer token; it never enters the URL or query.
func buildRequest(ctx context.Context, endpoint, apiKey string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	return req, nil
}

// getBalance calls GET /api/balance and decodes the response.
func getBalance(ctx context.Context, c httpDoer, apiKey string) (*balanceResponse, error) {
	var out balanceResponse
	if err := getJSON(ctx, c, balanceEndpoint, "/api/balance", apiKey, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// getUsage calls GET /api/usage?range=30d and decodes the response.
func getUsage(ctx context.Context, c httpDoer, apiKey string) (*usageResponse, error) {
	var out usageResponse
	if err := getJSON(ctx, c, usageEndpoint, "/api/usage", apiKey, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// getJSON performs the request and decodes a 200 body into out. name labels
// errors; they carry the status code but never the API key or the response body.
// 429 and 503 surface as ordinary status errors: the collection interval is far
// above the 10 requests/minute limit, so the next cycle is the retry.
func getJSON(ctx context.Context, c httpDoer, endpoint, name, apiKey string, out any) error {
	req, err := buildRequest(ctx, endpoint, apiKey)
	if err != nil {
		return fmt.Errorf("ollama: building request: %w", err)
	}

	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("ollama: calling %s: %w", name, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("ollama: reading %s response: %w", name, err)
	}

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrUnauthorized
	default:
		return fmt.Errorf("ollama: %s returned status %d", name, resp.StatusCode)
	}

	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("ollama: decoding %s response: %w", name, err)
	}
	return nil
}
