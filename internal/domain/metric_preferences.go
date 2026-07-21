package domain

import "sort"

// MetricPreferenceItem은 저장되는 metric 노출 설정이다.
type MetricPreferenceItem struct {
	Metric  string `json:"metric"`
	Visible bool   `json:"visible"`
}

// ReconciledMetricPreferenceItem은 현재 catalog를 반영한 metric 노출 설정이다.
type ReconciledMetricPreferenceItem struct {
	Metric    string `json:"metric"`
	Label     string `json:"label"`
	Visible   bool   `json:"visible"`
	Available bool   `json:"available"`
}

// ReconcileMetricPreferences는 저장 설정과 현재 catalog를 표시 가능한 설정으로 합친다.
func ReconcileMetricPreferences(saved []MetricPreferenceItem, catalog []string) []ReconciledMetricPreferenceItem {
	available := make(map[string]struct{}, len(catalog))
	for _, metric := range catalog {
		available[metric] = struct{}{}
	}

	result := make([]ReconciledMetricPreferenceItem, 0, len(saved)+len(catalog))
	seen := make(map[string]struct{}, len(saved)+len(catalog))
	for _, item := range saved {
		_, isAvailable := available[item.Metric]
		result = append(result, ReconciledMetricPreferenceItem{
			Metric:    item.Metric,
			Label:     MetricLabel(item.Metric),
			Visible:   item.Visible,
			Available: isAvailable,
		})
		seen[item.Metric] = struct{}{}
	}

	newMetrics := make([]string, 0, len(catalog))
	for _, metric := range catalog {
		if _, exists := seen[metric]; exists {
			continue
		}
		seen[metric] = struct{}{}
		newMetrics = append(newMetrics, metric)
	}
	sort.Strings(newMetrics)

	for _, metric := range newMetrics {
		result = append(result, ReconciledMetricPreferenceItem{
			Metric:    metric,
			Label:     MetricLabel(metric),
			Visible:   true,
			Available: true,
		})
	}

	return result
}
