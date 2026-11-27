// Package harness defines the boundary between Ember's core and coding-agent integrations.
package harness

import (
	"context"
	"io/fs"
)

// Adapter discovers a harness, describes its setup, and normalizes its local data.
// Implementations register themselves from init and do no direct file or process I/O.
type Adapter interface {
	Name() string
	Detect(env Env) Detection
	Setup(env Env, opts SetupOptions) []Change
	Sessions(env Env) ([]SessionRef, error)
	Parse(fsys fs.FS, ref SessionRef) (Run, error)
	Mapping() Mapping
}

// Env supplies everything an adapter needs from the process environment.
// Command implementations must enforce a five-second timeout.
type Env struct {
	Home string
	// FS is rooted at Home and supplies filesystem discovery without direct adapter I/O.
	FS       fs.FS
	OS       string
	Getenv   func(string) string
	LookPath func(string) (string, error)
	Command  func(ctx context.Context, name string, args ...string) (string, error)
}

// Detection describes a harness installation without changing it.
type Detection struct {
	Installed    bool
	Version      string
	ConfigPaths  []string
	SessionPaths []string
}

// SetupOptions contains the harness-neutral setup choices an adapter needs.
// Conflict handling, file writes, and shell rendering belong to the core.
type SetupOptions struct {
	ListenHTTP string
	FactoryApp bool
	Content    bool
}

// SessionRef identifies one run and every file that contributes to it.
// Files are slash-separated paths relative to Env.Home; Parse receives os.DirFS(Home).
type SessionRef struct {
	RunID string
	Files []string
}
