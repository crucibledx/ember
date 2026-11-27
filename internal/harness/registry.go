package harness

import (
	"fmt"
	"sort"
)

// adapters is populated only by init-time registrations and is read-only afterwards.
var adapters = map[string]Adapter{}

// Register adds an adapter to the process-wide registry.
// It panics on a duplicate name so conflicting init-time registrations fail immediately.
func Register(adapter Adapter) {
	name := adapter.Name()
	if _, exists := adapters[name]; exists {
		panic(fmt.Sprintf("harness adapter %q registered twice", name))
	}
	adapters[name] = adapter
}

// Adapters returns every registered adapter sorted by stable name.
func Adapters() []Adapter {
	result := make([]Adapter, 0, len(adapters))
	for _, adapter := range adapters {
		result = append(result, adapter)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name() < result[j].Name()
	})
	return result
}

// Lookup finds a registered adapter by its stable name.
func Lookup(name string) (Adapter, bool) {
	adapter, ok := adapters[name]
	return adapter, ok
}
