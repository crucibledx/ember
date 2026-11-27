package harness

import (
	"io/fs"
	"reflect"
	"strings"
	"testing"
)

type registryStub struct {
	name    string
	mapping Mapping
}

func (s registryStub) Name() string                       { return s.name }
func (registryStub) Detect(Env) Detection                 { return Detection{} }
func (registryStub) Setup(Env, SetupOptions) []Change     { return nil }
func (registryStub) Sessions(Env) ([]SessionRef, error)   { return nil, nil }
func (registryStub) Parse(fs.FS, SessionRef) (Run, error) { return Run{}, nil }
func (s registryStub) Mapping() Mapping                   { return s.mapping }

func TestRegistry(t *testing.T) {
	t.Run("duplicate name panics", func(t *testing.T) {
		resetRegistry(t)
		Register(registryStub{name: "duplicate"})

		defer func() {
			got := recover()
			if got == nil {
				t.Fatal("Register() did not panic for a duplicate name")
			}
			if !strings.Contains(got.(string), "duplicate") {
				t.Fatalf("panic %q does not name the duplicate", got)
			}
		}()
		Register(registryStub{name: "duplicate"})
	})

	t.Run("adapters sorted by name", func(t *testing.T) {
		resetRegistry(t)
		for _, name := range []string{"zeta", "alpha", "middle"} {
			Register(registryStub{name: name})
		}

		registered := Adapters()
		got := make([]string, 0, len(registered))
		for _, adapter := range registered {
			got = append(got, adapter.Name())
		}
		want := []string{"alpha", "middle", "zeta"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Adapters() names = %v, want %v", got, want)
		}
	})

	t.Run("lookup", func(t *testing.T) {
		resetRegistry(t)
		want := registryStub{name: "known"}
		Register(want)

		got, ok := Lookup("known")
		if !ok || got.Name() != want.Name() {
			t.Fatalf("Lookup(known) = %v, %t", got, ok)
		}
		if _, ok := Lookup("missing"); ok {
			t.Fatal("Lookup(missing) unexpectedly found an adapter")
		}
	})
}

func resetRegistry(t *testing.T) {
	t.Helper()
	previous := adapters
	adapters = map[string]Adapter{}
	t.Cleanup(func() {
		adapters = previous
	})
}
