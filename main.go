package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Data models
type Provider struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Used        int64      `json:"used"`
	Limit       int64      `json:"limit"`
	Remaining   int64      `json:"remaining"`
	ResetAt     time.Time  `json:"resetAt"`
	CollectedAt *time.Time `json:"collectedAt,omitempty"`
	LastError   string     `json:"lastError,omitempty"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

type TrendPoint struct {
	CollectedAt time.Time `json:"collectedAt"`
	ProviderID  string    `json:"providerId"`
	Metric      string    `json:"metric"`
	Used        int64     `json:"used"`
	Limit       int64     `json:"limit"`
}

type CurrentResponse struct {
	Providers []Provider `json:"providers"`
}

type TrendsResponse struct {
	Points []TrendPoint `json:"points"`
}

type ProvidersResponse struct {
	Providers []ProviderInfo `json:"providers"`
}

type ProviderInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

type HealthResponse struct {
	Status string `json:"status"` // "ok", "degraded", "error"
}

type DashboardData struct {
	Title        string
	Providers    []Provider
	HealthStatus string
	TrendData    *TrendsResponse
	Range        string
	HasError     bool
	ErrorMessage string
}

// Global state (in production, use proper caching/database)
var (
	currentState = struct {
		sync.RWMutex
		Providers []Provider
		Trends    map[string][]TrendPoint // keyed by range ("24h", "7d")
		LastError error
	}{
		Trends: make(map[string][]TrendPoint),
	}
)

func initSampleData() {
	now := time.Now()
	
	currentState.Providers = []Provider{
		{
			ID:          "openai",
			Name:        "OpenAI",
			Used:        int64(45000),
			Limit:       int64(100000),
			Remaining:   int64(55000),
			ResetAt:     time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC),
			CollectedAt: &now,
			LastError:   "",
			UpdatedAt:   now,
		},
		{
			ID:          "anthropic",
			Name:        "Anthropic",
			Used:        int64(82000),
			Limit:       int64(100000),
			Remaining:   int64(18000),
			ResetAt:     time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC),
			CollectedAt: &now,
			LastError:   "",
			UpdatedAt:   now,
		},
	}
	
	// Generate trend data for 24h/7d/30d
	for _, rangeName := range []string{"24h", "7d", "30d"} {
		var points []TrendPoint
		var interval time.Duration
		
		switch rangeName {
		case "24h":
			interval = time.Hour
			for i := 24; i >= 0; i-- {
				t := now.Add(-time.Duration(i) * interval)
				points = append(points, TrendPoint{
					CollectedAt: t,
					ProviderID:  "openai",
					Metric:      "usage",
					Used:        45000 - int64(i*1000),
					Limit:       100000,
				})
				points = append(points, TrendPoint{
					CollectedAt: t,
					ProviderID:  "anthropic",
					Metric:      "usage",
					Used:        82000 - int64(i*500),
					Limit:       100000,
				})
			}
		case "7d":
			interval = 24 * time.Hour
			for i := 7; i >= 0; i-- {
				t := now.Add(-time.Duration(i) * interval)
				points = append(points, TrendPoint{
					CollectedAt: t,
					ProviderID:  "openai",
					Metric:      "usage",
					Used:        45000 - int64(i*5000),
					Limit:       100000,
				})
				points = append(points, TrendPoint{
					CollectedAt: t,
					ProviderID:  "anthropic",
					Metric:      "usage",
					Used:        82000 - int64(i*3000),
					Limit:       100000,
				})
			}
		case "30d":
			interval = 24 * time.Hour
			for i := 30; i >= 0; i-- {
				t := now.Add(-time.Duration(i) * interval)
				points = append(points, TrendPoint{
					CollectedAt: t,
					ProviderID:  "openai",
					Metric:      "usage",
					Used:        45000 - int64(i*1000),
					Limit:       100000,
				})
				points = append(points, TrendPoint{
					CollectedAt: t,
					ProviderID:  "anthropic",
					Metric:      "usage",
					Used:        82000 - int64(i*800),
					Limit:       100000,
				})
			}
		}
		
		currentState.Trends[rangeName] = points
	}
}

func main() {
	// Check DEMO_MODE
	demoMode := os.Getenv("DEMO_MODE") == "true"
	if demoMode {
		log.Println("🔧 DEMO_MODE enabled - seeding sample data")
		initSampleData()
	}
	
	// Load templates
	tmpl := loadTemplates()

	// Setup routes
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		handleDashboard(tmpl, w, r)
	})

	http.HandleFunc("/api/current", handleAPICurrent)
	http.HandleFunc("/api/trends", handleAPITrends)
	http.HandleFunc("/api/providers", handleAPIProviders)
	http.HandleFunc("/healthz", handleHealthz)

	// Start server
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("🚀 AI 사용량 대시보드 서버 시작 (포트 %s)", port)
	log.Printf("📊 대시보드 URL: http://localhost:%s", port)
	
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("서버 시작 실패: %v", err)
	}
}

func loadTemplates() *template.Template {
	basePath := "templates"
	
	// Read component templates
	providerCard, err := os.ReadFile(filepath.Join(basePath, "components", "provider_card.html"))
	if err != nil {
		log.Fatalf("템플릿 로드 실패 (provider_card): %v", err)
	}

	trendChart, err := os.ReadFile(filepath.Join(basePath, "components", "trend_chart.html"))
	if err != nil {
		log.Fatalf("템플릿 로드 실패 (trend_chart): %v", err)
	}

	errorState, err := os.ReadFile(filepath.Join(basePath, "components", "error_state.html"))
	if err != nil {
		log.Fatalf("템플릿 로드 실패 (error_state): %v", err)
	}

	dashboard, err := os.ReadFile(filepath.Join(basePath, "dashboard.html"))
	if err != nil {
		log.Fatalf("템플릿 로드 실패 (dashboard): %v", err)
	}

	layout, err := os.ReadFile(filepath.Join(basePath, "layout.html"))
	if err != nil {
		log.Fatalf("템플릿 로드 실패 (layout): %v", err)
	}

	// Parse templates with custom functions
	funcMap := template.FuncMap{
		"formatNumber": func(n interface{}) string {
			switch v := n.(type) {
			case int64:
				return fmt.Sprintf("%d", v)
			case float64:
				return fmt.Sprintf("%.0f", v)
			default:
				return fmt.Sprintf("%v", v)
			}
		},
		"formatDateTime": func(t time.Time) string {
			if t.IsZero() {
				return "-"
			}
			return t.Format("1/2 15:04")
		},
		"getUsageClass": func(percentage float64) string {
			if percentage < 50 {
				return "progress-low"
			}
			if percentage < 80 {
				return "progress-medium"
			}
			return "progress-high"
		},
		"isStale": func(t *time.Time) bool {
			if t == nil || t.IsZero() {
				return true
			}
			return time.Since(*t) > 2*time.Hour
		},
		"float64": func(n int64) float64 {
			return float64(n)
		},
		"divf": func(a, b float64) float64 {
			if b == 0 {
				return 0
			}
			return a / b
		},
		"mul": func(a, b int64) int64 {
			return a * b
		},
		"mod": func(a, b int64) int64 {
			return a % b
		},
		"dict": func(values ...interface{}) map[string]interface{} {
			result := make(map[string]interface{})
			for i := 0; i < len(values)-1; i += 2 {
				if key, ok := values[i].(string); ok {
					result[key] = values[i+1]
				}
			}
			return result
		},
	}

	allContent := string(layout) + string(dashboard) + 
		string(providerCard) + string(trendChart) + string(errorState)

	tmpl, err := template.New("layout").Funcs(funcMap).Parse(allContent)
	if err != nil {
		log.Fatalf("템플릿 파싱 실패: %v", err)
	}

	return tmpl
}

func handleDashboard(tmpl *template.Template, w http.ResponseWriter, r *http.Request) {
	currentState.RLock()
	providers := currentState.Providers
	lastError := currentState.LastError
	currentState.RUnlock()

	data := DashboardData{
		Title:     "AI 사용량 대시보드",
		Providers: providers,
		Range:     "24h", // default
	}

	// Check for errors
	if lastError != nil {
		data.HasError = true
		data.ErrorMessage = lastError.Error()
	} else if len(providers) == 0 {
		data.HasError = true
		data.ErrorMessage = "수집된 데이터가 없습니다"
	}

	// Load trend data
	rangeParam := r.URL.Query().Get("range")
	if rangeParam == "" {
		rangeParam = "24h"
	}
	data.Range = rangeParam

	currentState.RLock()
	if trends, ok := currentState.Trends[rangeParam]; ok {
		data.TrendData = &TrendsResponse{Points: trends}
	}
	currentState.RUnlock()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, "layout", data); err != nil {
		log.Printf("템플릿 실행 오류: %v", err)
		http.Error(w, "내부 서버 오류", http.StatusInternalServerError)
	}
}

func handleAPICurrent(w http.ResponseWriter, r *http.Request) {
	currentState.RLock()
	providers := currentState.Providers
	currentState.RUnlock()

	response := CurrentResponse{Providers: providers}
	writeJSON(w, response)
}

func handleAPITrends(w http.ResponseWriter, r *http.Request) {
	rangeParam := r.URL.Query().Get("range")
	if rangeParam == "" {
		rangeParam = "24h"
	}

	if rangeParam != "24h" && rangeParam != "7d" && rangeParam != "30d" {
		http.Error(w, `{"error": "range must be '24h', '7d', or '30d'"}`, http.StatusBadRequest)
		return
	}

	currentState.RLock()
	points := currentState.Trends[rangeParam]
	currentState.RUnlock()

	if points == nil {
		points = []TrendPoint{}
	}

	response := TrendsResponse{Points: points}
	writeJSON(w, response)
}

func handleAPIProviders(w http.ResponseWriter, r *http.Request) {
	currentState.RLock()
	providers := currentState.Providers
	currentState.RUnlock()

	infos := make([]ProviderInfo, len(providers))
	for i, p := range providers {
		infos[i] = ProviderInfo{
			ID:      p.ID,
			Name:    p.Name,
			Enabled: true,
		}
	}

	response := ProvidersResponse{Providers: infos}
	writeJSON(w, response)
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	currentState.RLock()
	lastError := currentState.LastError
	providers := currentState.Providers
	currentState.RUnlock()

	status := "ok"
	if lastError != nil {
		status = "degraded"
	}
	if len(providers) == 0 && lastError != nil {
		status = "error"
	}

	response := HealthResponse{Status: status}
	writeJSON(w, response)
}

func writeJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("JSON 인코딩 오류: %v", err)
		http.Error(w, `{"error": "internal error"}`, http.StatusInternalServerError)
	}
}

// Background data collector (example - replace with actual API calls)
func startDataCollector() {
	ticker := time.NewTicker(30 * time.Second)
	go func() {
		for range ticker.C {
			collectData()
		}
	}()
}

func collectData() {
	// TODO: Implement actual data collection from AI providers
	// This is a placeholder for demonstration
	
	currentState.Lock()
	defer currentState.Unlock()
	
	// Example: Update with real provider data
	// currentState.Providers = fetchFromProviders()
	// currentState.Trends["24h"] = fetchTrends("24h")
	// currentState.Trends["7d"] = fetchTrends("7d")
	
	now := time.Now()
	if len(currentState.Providers) == 0 {
		// Sample data for testing
		currentState.Providers = []Provider{
			{
				ID:          "openai",
				Name:        "OpenAI",
				Used:        45000,
				Limit:       100000,
				Remaining:   55000,
				ResetAt:     now.Add(24 * time.Hour),
				CollectedAt: &now,
				UpdatedAt:   now,
			},
			{
				ID:          "anthropic",
				Name:        "Anthropic",
				Used:        82000,
				Limit:       100000,
				Remaining:   18000,
				ResetAt:     now.Add(24 * time.Hour),
				CollectedAt: &now,
				UpdatedAt:   now,
			},
			{
				ID:          "google",
				Name:        "Google AI",
				Used:        23000,
				Limit:       50000,
				Remaining:   27000,
				ResetAt:     now.Add(24 * time.Hour),
				CollectedAt: &now,
				UpdatedAt:   now,
			},
		}
		
		// Sample trend data
		var points24h, points7d, points30d []TrendPoint
		
		// 24h data (hourly)
		for i := 0; i < 24; i++ {
			t := now.Add(time.Duration(-i) * time.Hour)
			for _, p := range currentState.Providers {
				points24h = append(points24h, TrendPoint{
					CollectedAt: t,
					ProviderID:  p.ID,
					Metric:      "usage",
					Used:        p.Used - int64(i*1000),
					Limit:       p.Limit,
				})
			}
		}
		
		// 7d data (daily)
		for i := 0; i < 7; i++ {
			t := now.AddDate(0, 0, -i)
			for _, p := range currentState.Providers {
				points7d = append(points7d, TrendPoint{
					CollectedAt: t,
					ProviderID:  p.ID,
					Metric:      "usage",
					Used:        p.Used - int64(i*5000),
					Limit:       p.Limit,
				})
			}
		}
		
		// 30d data (daily)
		for i := 0; i < 30; i++ {
			t := now.AddDate(0, 0, -i)
			for _, p := range currentState.Providers {
				points30d = append(points30d, TrendPoint{
					CollectedAt: t,
					ProviderID:  p.ID,
					Metric:      "usage",
					Used:        p.Used - int64(i*1000),
					Limit:       p.Limit,
				})
			}
		}
		
		currentState.Trends["24h"] = points24h
		currentState.Trends["7d"] = points7d
		currentState.Trends["30d"] = points30d
	}
}
