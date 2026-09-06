package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	nethttp "net/http"
)

const providerOrderMaxBodyBytes = 64 * 1024

type providerOrderRequest struct {
	ProviderIDs []string `json:"provider_ids"`
}

type providerOrderResponse struct {
	ProviderIDs []string `json:"provider_ids"`
}

// handleProviderOrder saves the provider display order. The saved order
// governs the dashboard, the drawer, and every ListProviders consumer.
func (s *Server) handleProviderOrder(w nethttp.ResponseWriter, r *nethttp.Request) {
	if r.Method != nethttp.MethodPut {
		nethttp.Error(w, "Method not allowed", nethttp.StatusMethodNotAllowed)
		return
	}

	request, err := decodeProviderOrderRequest(w, r)
	if err != nil {
		s.jsonError(w, "Invalid provider order payload", nethttp.StatusBadRequest)
		return
	}

	providers, err := s.store.ListProviders()
	if err != nil {
		s.logger.Error("Failed to list providers for ordering", "error", err)
		s.jsonError(w, "Failed to save provider order", nethttp.StatusInternalServerError)
		return
	}

	registered := make(map[string]struct{}, len(providers))
	for _, provider := range providers {
		registered[provider.Name] = struct{}{}
	}

	if err := validateProviderOrder(request.ProviderIDs, registered); err != nil {
		s.jsonError(w, err.Error(), nethttp.StatusBadRequest)
		return
	}

	if err := s.store.SaveProviderOrder(request.ProviderIDs); err != nil {
		s.logger.Error("Failed to save provider order", "error", err)
		s.jsonError(w, "Failed to save provider order", nethttp.StatusInternalServerError)
		return
	}

	s.jsonResponse(w, providerOrderResponse(request))
}

func decodeProviderOrderRequest(w nethttp.ResponseWriter, r *nethttp.Request) (providerOrderRequest, error) {
	var request providerOrderRequest
	decoder := json.NewDecoder(nethttp.MaxBytesReader(w, r.Body, providerOrderMaxBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return request, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return request, fmt.Errorf("payload must contain exactly one JSON value")
	}
	if request.ProviderIDs == nil {
		request.ProviderIDs = []string{}
	}
	return request, nil
}

// validateProviderOrder requires a non-empty, duplicate-free list that contains
// every registered provider name exactly once.
func validateProviderOrder(submitted []string, registered map[string]struct{}) error {
	if len(submitted) == 0 {
		return fmt.Errorf("provider_ids must not be empty")
	}
	seen := make(map[string]struct{}, len(submitted))
	for _, name := range submitted {
		if _, ok := registered[name]; !ok {
			return fmt.Errorf("unknown provider %q", name)
		}
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("duplicate provider %q", name)
		}
		seen[name] = struct{}{}
	}
	if len(submitted) != len(registered) {
		return fmt.Errorf("provider_ids must contain every provider")
	}
	return nil
}
