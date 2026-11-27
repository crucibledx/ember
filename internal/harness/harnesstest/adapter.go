// Package harnesstest provides the shared Adapter conformance suite and a fake adapter.
package harnesstest

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"runtime"
	"strings"
	"testing"

	"github.com/crucibledx/ember/internal/harness"
)

// Run checks the behavior every Adapter implementation must share.
// Adapter-specific parsing and mapping values belong in that adapter's own fixture tests.
func Run(t *testing.T, newImpl func() harness.Adapter) {
	t.Helper()
	adapter := newImpl()

	t.Run("Name", func(t *testing.T) {
		if strings.TrimSpace(adapter.Name()) == "" {
			t.Fatal("Name() returned an empty name")
		}
	})
	t.Run("Detect", func(t *testing.T) {
		detection := adapter.Detect(emptyEnv(t))
		checkPaths(t, "config", detection.ConfigPaths)
		checkPaths(t, "session", detection.SessionPaths)
	})
	t.Run("Setup", func(t *testing.T) {
		changes := adapter.Setup(emptyEnv(t), harness.SetupOptions{
			ListenHTTP: "127.0.0.1:4318",
		})
		for i, change := range changes {
			checkChange(t, i, change)
		}
	})
	t.Run("Sessions", func(t *testing.T) {
		refs, err := adapter.Sessions(emptyEnv(t))
		if err != nil {
			t.Fatalf("Sessions() on an empty home: %v", err)
		}
		for _, ref := range refs {
			checkSessionRef(t, ref)
		}
	})
	t.Run("Mapping", func(t *testing.T) {
		checkMapping(t, adapter.Mapping())
	})
}

func emptyEnv(t *testing.T) harness.Env {
	t.Helper()
	return harness.Env{
		Home: t.TempDir(),
		OS:   runtime.GOOS,
		Getenv: func(string) string {
			return ""
		},
		LookPath: func(string) (string, error) {
			return "", errors.New("not installed")
		},
		Command: func(context.Context, string, ...string) (string, error) {
			return "", errors.New("not installed")
		},
	}
}

func checkPaths(t *testing.T, kind string, paths []string) {
	t.Helper()
	for _, path := range paths {
		if path == "" {
			t.Errorf("Detect() returned an empty %s path", kind)
		}
	}
}

func checkChange(t *testing.T, index int, change harness.Change) {
	t.Helper()
	switch change.Target {
	case harness.EnvShell:
		if change.Key == "" {
			t.Errorf("change %d has an empty environment key", index)
		}
	case harness.EnvFile, harness.JSONKey:
		if change.Path == "" || change.Key == "" {
			t.Errorf("change %d needs a path and key, got path %q and key %q", index, change.Path, change.Key)
		}
	default:
		t.Errorf("change %d has unknown target %q", index, change.Target)
	}
	for _, osName := range change.OS {
		if osName == "" {
			t.Errorf("change %d has an empty OS", index)
		}
	}
	checkSignal(t, index, change)
}

func checkSignal(t *testing.T, index int, change harness.Change) {
	t.Helper()
	switch change.Signal {
	case "", "traces", "metrics", "logs":
	default:
		t.Errorf("change %d has unknown signal %q", index, change.Signal)
	}
}

func checkSessionRef(t *testing.T, ref harness.SessionRef) {
	t.Helper()
	if err := validateSessionRef(ref); err != nil {
		t.Error(err)
	}
}

func validateSessionRef(ref harness.SessionRef) error {
	if ref.RunID == "" {
		return errors.New("Sessions() returned an empty run id")
	}
	if len(ref.Files) == 0 {
		return fmt.Errorf("session %q has no files", ref.RunID)
	}
	for _, name := range ref.Files {
		if !fs.ValidPath(name) || name == "." || strings.ContainsRune(name, '\\') || strings.Contains(name, ":") {
			return fmt.Errorf("session %q has invalid relative slash path %q", ref.RunID, name)
		}
	}
	return nil
}

func checkMapping(t *testing.T, mapping harness.Mapping) {
	t.Helper()
	for key, values := range mapping.Resource {
		if key == "" {
			t.Error("Mapping.Resource contains an empty attribute")
		}
		for _, value := range values {
			if value == "" {
				t.Errorf("Mapping.Resource[%q] contains an empty value", key)
			}
		}
	}
	if mapping.Metrics.Cost == "" && mapping.Metrics.CostUnit != "" {
		t.Error("Mapping.Metrics has a cost unit without a metric name")
	}
}
