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

// usageEndpoint is the Ollama Cloud usage endpoint. It is fixed rather than
// configurable: the API key is account-scoped to ollama.com, so a redirectable
// host would only widen where that credential can be sent.
const usageEndpoint = "https://ollama.com/api/usage"

// userAgent is the header value identifying the webusage version.
const userAgent = "webusage/1.0.0"

// ErrUnauthorized indicates the API key was rejected. It is distinct from a
// transport failure so the operator knows to fix OLLAMA_API_KEY rather than
// wait for the next collection cycle.
var ErrUnauthorized = errors.New("ollama: API key rejected; check OLLAMA_API_KEY")

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

// buildUsageRequest builds the GET /api/usage request.
// The API key is sent only as a bearer token; it never enters the URL or query.
func buildUsageRequest(ctx context.Context, apiKey string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, usageEndpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	return req, nil
}

// getUsage calls GET /api/usage and decodes the response.
// Errors carry the status code but never the API key or the response body.
func getUsage(ctx context.Context, c httpDoer, apiKey string) (*usageResponse, error) {
	req, err := buildUsageRequest(ctx, apiKey)
	if err != nil {
		return nil, fmt.Errorf("ollama: building request: %w", err)
	}

	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama: calling /api/usage: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("ollama: reading response: %w", err)
	}

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, ErrUnauthorized
	default:
		return nil, fmt.Errorf("ollama: /api/usage returned status %d", resp.StatusCode)
	}

	var out usageResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("ollama: decoding response: %w", err)
	}
	return &out, nil
}
