package domain

import "testing"

func TestResolvePrimaryMetricShouldPickHeadlineFromStoredMetrics(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		stored   []string
		want     string
	}{
		// A current-plan account may keep a stale weekly row from its legacy plan.
		{"ollama current plan", "ollama", []string{"cost", "credits", "session", "weekly"}, "credits"},
		{"ollama legacy plan", "ollama", []string{"cost", "session", "weekly"}, "weekly"},
		{"ollama nothing stored", "ollama", nil, "weekly"},
		{"provider without preferences", "claude", []string{"session", "weekly"}, "session"},
		{"unknown provider", "unknown", []string{"other"}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given / When
			got := ResolvePrimaryMetric(tt.provider, tt.stored)

			// Then
			if got != tt.want {
				t.Errorf("ResolvePrimaryMetric(%q, %v) = %q, want %q", tt.provider, tt.stored, got, tt.want)
			}
		})
	}
}
