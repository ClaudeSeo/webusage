package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	nethttp "net/http"

	"github.com/ClaudeSeo/webusage/internal/domain"
	"github.com/ClaudeSeo/webusage/internal/store"
)

const metricPreferenceMaxBodyBytes = 64 * 1024

type metricPreferencePutRequest struct {
	Providers []metricPreferenceProviderRequest `json:"providers"`
}

type metricPreferenceProviderRequest struct {
	ProviderID      string                        `json:"provider_id"`
	ExpectedVersion int64                         `json:"expected_version"`
	Items           []domain.MetricPreferenceItem `json:"items"`
}

type metricPreferenceResponse struct {
	Error     string                             `json:"error,omitempty"`
	Providers []metricPreferenceProviderResponse `json:"providers"`
}

type metricPreferenceProviderResponse struct {
	ProviderID string                                  `json:"provider_id"`
	Version    int64                                   `json:"version"`
	Items      []domain.ReconciledMetricPreferenceItem `json:"items"`
}

func (s *Server) handleMetricPreferences(w nethttp.ResponseWriter, r *nethttp.Request) {
	switch r.Method {
	case nethttp.MethodGet:
		s.handleGetMetricPreferences(w)
	case nethttp.MethodPut:
		s.handlePutMetricPreferences(w, r)
	default:
		nethttp.Error(w, "Method not allowed", nethttp.StatusMethodNotAllowed)
	}
}

func (s *Server) handleGetMetricPreferences(w nethttp.ResponseWriter) {
	providers, err := s.store.ListProviders()
	if err != nil {
		s.logger.Error("Failed to list metric preference providers", "error", err)
		s.jsonError(w, "Failed to list metric preferences", nethttp.StatusInternalServerError)
		return
	}

	responseProviders, err := s.metricPreferenceResponses(providers)
	if err != nil {
		s.logger.Error("Failed to compose metric preferences", "error", err)
		s.jsonError(w, "Failed to load metric preferences", nethttp.StatusInternalServerError)
		return
	}

	s.jsonResponse(w, metricPreferenceResponse{Providers: responseProviders})
}

func (s *Server) handlePutMetricPreferences(w nethttp.ResponseWriter, r *nethttp.Request) {
	request, err := decodeMetricPreferenceRequest(w, r)
	if err != nil {
		s.jsonError(w, "Invalid metric preference payload", nethttp.StatusBadRequest)
		return
	}

	providers, err := s.store.ListProviders()
	if err != nil {
		s.logger.Error("Failed to list metric preference providers", "error", err)
		s.jsonError(w, "Failed to validate metric preferences", nethttp.StatusInternalServerError)
		return
	}
	providersByName := make(map[string]*store.Provider, len(providers))
	for _, provider := range providers {
		providersByName[provider.Name] = provider
	}

	updates, submittedProviders, err := s.validateMetricPreferenceUpdates(request, providersByName)
	if err != nil {
		s.jsonError(w, err.Error(), nethttp.StatusBadRequest)
		return
	}

	if err := s.store.SaveMetricPreferences(updates); err != nil {
		if errors.Is(err, store.ErrMetricPreferenceVersionConflict) {
			s.writeMetricPreferenceConflict(w, submittedProviders)
			return
		}
		s.logger.Error("Failed to save metric preferences", "error", err)
		s.jsonError(w, "Failed to save metric preferences", nethttp.StatusInternalServerError)
		return
	}

	responseProviders, err := s.metricPreferenceResponses(submittedProviders)
	if err != nil {
		s.logger.Error("Failed to compose saved metric preferences", "error", err)
		s.jsonError(w, "Failed to load saved metric preferences", nethttp.StatusInternalServerError)
		return
	}
	s.jsonResponse(w, metricPreferenceResponse{Providers: responseProviders})
}

func decodeMetricPreferenceRequest(w nethttp.ResponseWriter, r *nethttp.Request) (metricPreferencePutRequest, error) {
	var request metricPreferencePutRequest
	decoder := json.NewDecoder(nethttp.MaxBytesReader(w, r.Body, metricPreferenceMaxBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return request, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return request, fmt.Errorf("payload must contain exactly one JSON value")
	}
	if request.Providers == nil {
		request.Providers = []metricPreferenceProviderRequest{}
	}
	return request, nil
}

func (s *Server) validateMetricPreferenceUpdates(
	request metricPreferencePutRequest,
	providersByName map[string]*store.Provider,
) ([]store.MetricPreferenceUpdate, []*store.Provider, error) {
	updates := make([]store.MetricPreferenceUpdate, 0, len(request.Providers))
	submittedProviders := make([]*store.Provider, 0, len(request.Providers))
	seenProviders := make(map[string]struct{}, len(request.Providers))

	for _, submitted := range request.Providers {
		if _, duplicate := seenProviders[submitted.ProviderID]; duplicate {
			return nil, nil, fmt.Errorf("duplicate provider_id %q", submitted.ProviderID)
		}
		seenProviders[submitted.ProviderID] = struct{}{}

		provider, exists := providersByName[submitted.ProviderID]
		if !exists {
			return nil, nil, fmt.Errorf("unknown provider_id %q", submitted.ProviderID)
		}
		if submitted.ExpectedVersion < 0 {
			return nil, nil, fmt.Errorf("expected_version must not be negative for provider %q", submitted.ProviderID)
		}

		stored, err := s.store.GetMetricPreference(provider.ID)
		if err != nil {
			return nil, nil, fmt.Errorf("loading existing metric preference for provider %q: %w", submitted.ProviderID, err)
		}
		catalog, err := s.metricCatalog(provider.ID)
		if err != nil {
			return nil, nil, fmt.Errorf("loading metric catalog for provider %q: %w", submitted.ProviderID, err)
		}

		knownMetrics := make(map[string]struct{}, len(catalog)+len(stored.Items))
		for _, metric := range catalog {
			knownMetrics[metric] = struct{}{}
		}
		for _, item := range stored.Items {
			knownMetrics[item.Metric] = struct{}{}
		}

		seenMetrics := make(map[string]struct{}, len(submitted.Items))
		for _, item := range submitted.Items {
			if item.Metric == "" {
				return nil, nil, fmt.Errorf("metric key must not be empty for provider %q", submitted.ProviderID)
			}
			if _, duplicate := seenMetrics[item.Metric]; duplicate {
				return nil, nil, fmt.Errorf("duplicate metric key %q for provider %q", item.Metric, submitted.ProviderID)
			}
			seenMetrics[item.Metric] = struct{}{}
			if _, known := knownMetrics[item.Metric]; !known {
				return nil, nil, fmt.Errorf("unknown metric key %q for provider %q", item.Metric, submitted.ProviderID)
			}
		}

		mergedItems := mergeMetricPreferenceItems(stored.Items, submitted.Items, catalog)
		canonicalItems := domain.ReconcileMetricPreferences(mergedItems, catalog)
		if len(catalog) > 0 && !hasVisibleAvailableMetric(canonicalItems) {
			return nil, nil, fmt.Errorf("provider %q must keep at least one available metric visible", submitted.ProviderID)
		}

		itemsToStore := make([]domain.MetricPreferenceItem, len(canonicalItems))
		for index, item := range canonicalItems {
			itemsToStore[index] = domain.MetricPreferenceItem{Metric: item.Metric, Visible: item.Visible}
		}
		updates = append(updates, store.MetricPreferenceUpdate{
			ProviderID:      provider.ID,
			ExpectedVersion: submitted.ExpectedVersion,
			Items:           itemsToStore,
		})
		submittedProviders = append(submittedProviders, provider)
	}

	return updates, submittedProviders, nil
}

func hasVisibleAvailableMetric(items []domain.ReconciledMetricPreferenceItem) bool {
	for _, item := range items {
		if item.Available && item.Visible {
			return true
		}
	}
	return false
}

func mergeMetricPreferenceItems(
	stored []domain.MetricPreferenceItem,
	submitted []domain.MetricPreferenceItem,
	catalog []string,
) []domain.MetricPreferenceItem {
	available := make(map[string]struct{}, len(catalog))
	for _, metric := range catalog {
		available[metric] = struct{}{}
	}

	submittedAvailable := make([]domain.MetricPreferenceItem, 0, len(submitted))
	submittedAvailableKeys := make(map[string]struct{}, len(submitted))
	for _, item := range submitted {
		if _, exists := available[item.Metric]; !exists {
			continue
		}
		submittedAvailable = append(submittedAvailable, item)
		submittedAvailableKeys[item.Metric] = struct{}{}
	}

	replacements := append([]domain.MetricPreferenceItem(nil), submittedAvailable...)
	for _, item := range stored {
		if _, exists := available[item.Metric]; !exists {
			continue
		}
		if _, submitted := submittedAvailableKeys[item.Metric]; submitted {
			continue
		}
		replacements = append(replacements, item)
	}

	merged := make([]domain.MetricPreferenceItem, 0, len(stored)+len(replacements))
	replacementIndex := 0
	for _, item := range stored {
		if _, exists := available[item.Metric]; !exists {
			// unavailable 항목은 client payload와 무관하게 내부 slot과 visibility를 보존한다.
			merged = append(merged, item)
			continue
		}
		if replacementIndex < len(replacements) {
			merged = append(merged, replacements[replacementIndex])
			replacementIndex++
		}
	}
	merged = append(merged, replacements[replacementIndex:]...)
	return merged
}

func (s *Server) metricCatalog(providerID int64) ([]string, error) {
	snapshots, err := s.store.GetLatestUsageByProvider(providerID)
	if err != nil {
		return nil, err
	}
	catalog := make([]string, len(snapshots))
	for index, snapshot := range snapshots {
		catalog[index] = snapshot.Metric
	}
	return catalog, nil
}

func (s *Server) metricPreferenceResponses(providers []*store.Provider) ([]metricPreferenceProviderResponse, error) {
	responses := make([]metricPreferenceProviderResponse, 0, len(providers))
	for _, provider := range providers {
		preference, err := s.store.GetMetricPreference(provider.ID)
		if err != nil {
			return nil, fmt.Errorf("loading provider %q preference: %w", provider.Name, err)
		}
		catalog, err := s.metricCatalog(provider.ID)
		if err != nil {
			return nil, fmt.Errorf("loading provider %q catalog: %w", provider.Name, err)
		}
		responses = append(responses, metricPreferenceProviderResponse{
			ProviderID: provider.Name,
			Version:    preference.Version,
			Items:      domain.ReconcileMetricPreferences(preference.Items, catalog),
		})
	}
	return responses, nil
}

func (s *Server) writeMetricPreferenceConflict(w nethttp.ResponseWriter, providers []*store.Provider) {
	responseProviders, err := s.metricPreferenceResponses(providers)
	if err != nil {
		s.logger.Error("Failed to compose conflicting metric preferences", "error", err)
		s.jsonError(w, "Failed to load latest metric preferences", nethttp.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(nethttp.StatusConflict)
	_ = json.NewEncoder(w).Encode(metricPreferenceResponse{
		Error:     "Metric preferences changed; reload the latest settings",
		Providers: responseProviders,
	})
}
