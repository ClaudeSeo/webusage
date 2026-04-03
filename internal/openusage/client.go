// Package openusage provides a client for the OpenUsage local HTTP API.
// OpenUsage exposes usage data from various AI providers via a simple HTTP API.
package openusage

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// DefaultURL is the default OpenUsage API endpoint
const DefaultURL = "http://127.0.0.1:6736"

// Client is an OpenUsage API client
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a new OpenUsage client
func NewClient(baseURL string) *Client {
	if baseURL == "" {
		baseURL = DefaultURL
	}
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// UsageSnapshot represents a single provider's usage data from OpenUsage
type UsageSnapshot struct {
	ProviderID  string    `json:"providerId"`
	DisplayName string    `json:"displayName"`
	Plan        string    `json:"plan"`
	Lines       []Line    `json:"lines"`
	FetchedAt   time.Time `json:"fetchedAt"`
}

// Line represents a single metric line in the usage snapshot
type Line struct {
	Type             string  `json:"type"`             // "progress" or "text"
	Label            string  `json:"label"`            // "Session", "Weekly", "Today" 등
	Used             float64 `json:"used,omitempty"`   // progress type only
	Limit            float64 `json:"limit,omitempty"`  // progress type only
	Format           *Format `json:"format,omitempty"` // progress type only
	ResetsAt         *string `json:"resetsAt,omitempty"`
	PeriodDurationMs *int64  `json:"periodDurationMs,omitempty"`
	Value            string  `json:"value,omitempty"` // text type only
	Color            *string `json:"color,omitempty"`
}

// Format describes how to display a progress value
type Format struct {
	Kind   string `json:"kind"`   // "percent" or "count"
	Suffix string `json:"suffix"` // e.g., "credits"
}

// GetAllUsage fetches all provider usage data from OpenUsage
func (c *Client) GetAllUsage() ([]UsageSnapshot, error) {
	resp, err := c.httpClient.Get(c.baseURL + "/v1/usage")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch usage: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var snapshots []UsageSnapshot
	if err := json.Unmarshal(body, &snapshots); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return snapshots, nil
}

// GetProviderUsage fetches usage data for a specific provider
func (c *Client) GetProviderUsage(providerID string) (*UsageSnapshot, error) {
	resp, err := c.httpClient.Get(c.baseURL + "/v1/usage/" + providerID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch usage for %s: %w", providerID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		// Provider exists but has no cached data
		return nil, nil
	}

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("provider %s not found", providerID)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var snapshot UsageSnapshot
	if err := json.Unmarshal(body, &snapshot); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &snapshot, nil
}

// IsHealthy checks if OpenUsage API is available
func (c *Client) IsHealthy() bool {
	resp, err := c.httpClient.Get(c.baseURL + "/v1/usage")
	if err != nil {
		return false
	}
	defer func() {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()
	return resp.StatusCode == http.StatusOK
}