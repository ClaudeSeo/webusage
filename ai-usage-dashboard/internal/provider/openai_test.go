package provider

import (
	"context"
	"testing"
	"time"
)

func TestOpenAIProvider_Name(t *testing.T) {
	p := NewOpenAIProvider(ProviderConfig{APIKey: "test-key"})
	if p.Name() != "openai" {
		t.Errorf("Expected name 'openai', got '%s'", p.Name())
	}
}

func TestOpenAIProvider_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  ProviderConfig
		wantErr bool
	}{
		{
			name:    "valid config",
			config:  ProviderConfig{APIKey: "sk-test"},
			wantErr: false,
		},
		{
			name:    "missing api key",
			config:  ProviderConfig{},
			wantErr: true,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewOpenAIProvider(tt.config)
			err := p.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestOpenAIProvider_FetchUsage_Placeholder(t *testing.T) {
	p := NewOpenAIProvider(ProviderConfig{APIKey: "test-key"})
	ctx := context.Background()
	
	points, err := p.FetchUsage(ctx)
	if err != nil {
		t.Errorf("FetchUsage() unexpected error: %v", err)
	}
	
	if len(points) != 1 {
		t.Errorf("Expected 1 usage point, got %d", len(points))
	}
	
	if points[0].Metric != "info" {
		t.Errorf("Expected metric 'info', got '%s'", points[0].Metric)
	}
}

func TestAnthropicProvider_Name(t *testing.T) {
	p := NewAnthropicProvider(ProviderConfig{APIKey: "test-key"})
	if p.Name() != "anthropic" {
		t.Errorf("Expected name 'anthropic', got '%s'", p.Name())
	}
}

func TestAnthropicProvider_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  ProviderConfig
		wantErr bool
	}{
		{
			name:    "valid config",
			config:  ProviderConfig{APIKey: "sk-ant-test"},
			wantErr: false,
		},
		{
			name:    "missing api key",
			config:  ProviderConfig{},
			wantErr: true,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewAnthropicProvider(tt.config)
			err := p.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestAnthropicProvider_FetchUsage_Placeholder(t *testing.T) {
	p := NewAnthropicProvider(ProviderConfig{APIKey: "test-key"})
	ctx := context.Background()
	
	points, err := p.FetchUsage(ctx)
	if err != nil {
		t.Errorf("FetchUsage() unexpected error: %v", err)
	}
	
	if len(points) != 1 {
		t.Errorf("Expected 1 usage point, got %d", len(points))
	}
	
	if points[0].Metric != "info" {
		t.Errorf("Expected metric 'info', got '%s'", points[0].Metric)
	}
	
	if points[0].CollectedAt.IsZero() {
		t.Error("Expected non-zero CollectedAt")
	}
	
	// Should be within last second
	if time.Since(points[0].CollectedAt) > time.Second {
		t.Error("CollectedAt should be recent")
	}
}

func TestAnthropicProvider_RecordUsage(t *testing.T) {
	p := NewAnthropicProvider(ProviderConfig{APIKey: "test-key"})
	
	tokens := InputOutputTokens{Input: 100, Output: 50}
	point := p.RecordUsage(tokens)
	
	if point.Metric != "tokens" {
		t.Errorf("Expected metric 'tokens', got '%s'", point.Metric)
	}
	
	expectedTotal := float64(100 + 50)
	if point.Used != expectedTotal {
		t.Errorf("Expected used %f, got %f", expectedTotal, point.Used)
	}
}
