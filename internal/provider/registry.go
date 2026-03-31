package provider

import (
	"fmt"
	"sync"
)

// Registry는 provider 인스턴스를 관리하는 중앙 레지스트리
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
	// enabled는 활성화된 provider 이름 목록
	enabled map[string]bool
}

// NewRegistry는 빈 Registry를 생성합니다
func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[string]Provider),
		enabled:   make(map[string]bool),
	}
}

// Register는 provider를 레지스트리에 등록합니다
// 같은 이름으로 재등록하면 기존 provider를 덮어씁니다
func (r *Registry) Register(p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := p.Name()
	r.providers[name] = p
	// 등록 시 기본으로 활성화
	r.enabled[name] = true
}

// Get은 이름으로 provider를 조회합니다
// 존재하지 않으면 nil, false 반환
func (r *Registry) Get(name string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.providers[name]
	return p, ok
}

// MustGet은 이름으로 provider를 조회합니다
// 존재하지 않으면 panic 대신 error 반환
func (r *Registry) MustGet(name string) (Provider, error) {
	p, ok := r.Get(name)
	if !ok {
		return nil, fmt.Errorf("provider %q not found in registry", name)
	}
	return p, nil
}

// List는 등록된 모든 provider를 반환합니다
func (r *Registry) List() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]Provider, 0, len(r.providers))
	for _, p := range r.providers {
		result = append(result, p)
	}
	return result
}

// ListEnabled는 활성화된 provider만 반환합니다
func (r *Registry) ListEnabled() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]Provider, 0)
	for name, p := range r.providers {
		if r.enabled[name] {
			result = append(result, p)
		}
	}
	return result
}

// SetEnabled는 provider의 활성화 상태를 변경합니다
func (r *Registry) SetEnabled(name string, enabled bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.providers[name]; !ok {
		return fmt.Errorf("provider %q not found in registry", name)
	}
	r.enabled[name] = enabled
	return nil
}

// IsEnabled는 provider가 활성화되어 있는지 반환합니다
func (r *Registry) IsEnabled(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.enabled[name]
}
