package provider

import (
	"context"
	"testing"
)

// mockProvider는 테스트용 Provider 구현체
type mockProvider struct {
	name        string
	displayName string
}

func (m *mockProvider) Name() string                 { return m.name }
func (m *mockProvider) DisplayName() string          { return m.displayName }
func (m *mockProvider) AuthMethod() AuthMethod       { return AuthOAuthFile }
func (m *mockProvider) NeedsAuth() bool              { return false }
func (m *mockProvider) DiscoverCredentials(_ context.Context) (bool, error) { return true, nil }
func (m *mockProvider) RefreshAuth(_ context.Context) error { return nil }
func (m *mockProvider) FetchUsage(_ context.Context) ([]UsagePoint, error) {
	return []UsagePoint{}, nil
}
func (m *mockProvider) FetchSubscription(_ context.Context) (*SubscriptionInfo, error) {
	return &SubscriptionInfo{}, nil
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()
	p := &mockProvider{name: "test", displayName: "Test"}

	r.Register(p)

	got, ok := r.Get("test")
	if !ok {
		t.Fatal("Get() should find registered provider")
	}
	if got.Name() != "test" {
		t.Errorf("Get() Name = %q, want %q", got.Name(), "test")
	}
}

func TestRegistry_GetNotFound(t *testing.T) {
	r := NewRegistry()
	_, ok := r.Get("nonexistent")
	if ok {
		t.Error("Get() should return false for unregistered provider")
	}
}

func TestRegistry_MustGet(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockProvider{name: "test"})

	_, err := r.MustGet("test")
	if err != nil {
		t.Errorf("MustGet() unexpected error: %v", err)
	}

	_, err = r.MustGet("nonexistent")
	if err == nil {
		t.Error("MustGet() should return error for unregistered provider")
	}
}

func TestRegistry_List(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockProvider{name: "a"})
	r.Register(&mockProvider{name: "b"})
	r.Register(&mockProvider{name: "c"})

	list := r.List()
	if len(list) != 3 {
		t.Errorf("List() returned %d providers, want 3", len(list))
	}
}

func TestRegistry_ListEnabled(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockProvider{name: "enabled1"})
	r.Register(&mockProvider{name: "enabled2"})
	r.Register(&mockProvider{name: "disabled"})

	if err := r.SetEnabled("disabled", false); err != nil {
		t.Fatalf("SetEnabled() error: %v", err)
	}

	enabled := r.ListEnabled()
	if len(enabled) != 2 {
		t.Errorf("ListEnabled() returned %d providers, want 2", len(enabled))
	}
}

func TestRegistry_SetEnabled(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockProvider{name: "test"})

	// 기본 활성화 상태 확인
	if !r.IsEnabled("test") {
		t.Error("IsEnabled() should be true after registration")
	}

	// 비활성화
	if err := r.SetEnabled("test", false); err != nil {
		t.Fatalf("SetEnabled() error: %v", err)
	}
	if r.IsEnabled("test") {
		t.Error("IsEnabled() should be false after SetEnabled(false)")
	}

	// 다시 활성화
	if err := r.SetEnabled("test", true); err != nil {
		t.Fatalf("SetEnabled() error: %v", err)
	}
	if !r.IsEnabled("test") {
		t.Error("IsEnabled() should be true after SetEnabled(true)")
	}
}

func TestRegistry_SetEnabled_NotFound(t *testing.T) {
	r := NewRegistry()
	err := r.SetEnabled("nonexistent", false)
	if err == nil {
		t.Error("SetEnabled() should return error for unregistered provider")
	}
}

func TestRegistry_Override(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockProvider{name: "test", displayName: "Original"})
	r.Register(&mockProvider{name: "test", displayName: "Override"})

	p, _ := r.Get("test")
	if p.DisplayName() != "Override" {
		t.Errorf("DisplayName() = %q, want %q", p.DisplayName(), "Override")
	}
	// 재등록해도 수는 1개 유지
	if len(r.List()) != 1 {
		t.Errorf("List() returned %d, want 1 after override", len(r.List()))
	}
}
