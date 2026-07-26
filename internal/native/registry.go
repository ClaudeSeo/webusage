package native

// Registry holds the native providers that the collector runs.
type Registry struct {
	providers []Provider
}

// NewRegistry builds a registry from the given providers (order preserved).
func NewRegistry(providers ...Provider) *Registry {
	return &Registry{providers: providers}
}

// Providers returns the registered providers.
func (r *Registry) Providers() []Provider {
	return r.providers
}
