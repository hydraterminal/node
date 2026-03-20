package source

import (
	"fmt"
	"sync"
)

// SourceFactory creates a new instance of a Source.
type SourceFactory func() Source

var (
	mu       sync.RWMutex
	registry = map[string]SourceFactory{}
)

// Register adds a source factory to the registry.
// Called by each source package's init() function.
func Register(name string, factory SourceFactory) {
	mu.Lock()
	defer mu.Unlock()
	if _, exists := registry[name]; exists {
		panic(fmt.Sprintf("source already registered: %s", name))
	}
	registry[name] = factory
}

// Create instantiates a source by name.
func Create(name string) (Source, error) {
	mu.RLock()
	defer mu.RUnlock()
	factory, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("unknown source: %s", name)
	}
	return factory(), nil
}

// List returns the names of all registered sources.
func List() []string {
	mu.RLock()
	defer mu.RUnlock()
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	return names
}

// Has checks if a source is registered.
func Has(name string) bool {
	mu.RLock()
	defer mu.RUnlock()
	_, ok := registry[name]
	return ok
}
