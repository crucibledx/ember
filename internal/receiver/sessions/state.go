package sessions

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/crucibledx/ember/internal/harness"
	"github.com/crucibledx/ember/internal/tree"
)

type fileState struct {
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mtime"`
}
type runState struct {
	Files           map[string]fileState `json:"files"`
	Run             harness.Run          `json:"run"`
	LastSegment     int                  `json:"last_segment"`
	EmittedSegments []int                `json:"emitted_segments,omitempty"`
	Tools           []string             `json:"tools"`
	LogTime         time.Time            `json:"log_time"`
	LogOrdinal      int                  `json:"log_ordinal"`
	AcceptedLogs    []string             `json:"accepted_logs,omitempty"`
	FinalMetrics    bool                 `json:"final_metrics"`
	NativeTools     map[string]string    `json:"native_tools,omitempty"`
}
type diskState struct {
	Version    int                  `json:"version"`
	Runs       map[string]*runState `json:"runs"`
	Roots      tree.RootPins        `json:"roots"`
	Native     map[string]time.Time `json:"native_first_seen"`
	NativeLast map[string]time.Time `json:"native_last_seen,omitempty"`
}

func emptyState() *diskState {
	return &diskState{Version: 1, Runs: map[string]*runState{}, Roots: tree.RootPins{Roots: map[string]string{}, Missing: map[string]time.Time{}}, Native: map[string]time.Time{}, NativeLast: map[string]time.Time{}}
}
func stateKey(harnessName, runID string) string {
	return tree.RootRunID(harnessName, runID)
}
func loadState(home string) (*diskState, error) {
	body, err := os.ReadFile(filepath.Join(home, "state", "sessions.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return emptyState(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read session state: %w", err)
	}
	loaded := &diskState{}
	if err := json.Unmarshal(body, loaded); err != nil {
		return nil, fmt.Errorf("decode session state: %w", err)
	}
	if loaded.Version != 1 || loaded.Runs == nil {
		return nil, errors.New("decode session state: unsupported schema or null runs")
	}
	for key, state := range loaded.Runs {
		if state == nil || state.Run.Harness == "" || state.Run.SessionID == "" || state.Run.StartedAt.IsZero() {
			return nil, fmt.Errorf("decode session state: invalid run %q", key)
		}
		qualified := stateKey(state.Run.Harness, state.Run.SessionID)
		if key != qualified {
			if key != state.Run.SessionID {
				return nil, fmt.Errorf("decode session state: mismatched run key %q", key)
			}
			delete(loaded.Runs, key)
			loaded.Runs[qualified] = state
			for nativeKey, native := range loaded.Native {
				if nativeKey == key {
					loaded.Native[qualified] = native
					delete(loaded.Native, nativeKey)
				}
			}
			for nativeKey, native := range loaded.NativeLast {
				if nativeKey == key {
					loaded.NativeLast[qualified] = native
					delete(loaded.NativeLast, nativeKey)
				}
			}
		}
	}
	migrateRootPins(loaded)
	if loaded.Native == nil {
		loaded.Native = map[string]time.Time{}
	}
	if loaded.NativeLast == nil {
		loaded.NativeLast = map[string]time.Time{}
	}
	return loaded, nil
}
func migrateRootPins(state *diskState) {
	roots := map[string]string{}
	missing := map[string]time.Time{}
	for _, run := range state.Runs {
		harnessName, id := run.Run.Harness, run.Run.SessionID
		key := stateKey(harnessName, id)
		if root, ok := state.Roots.Roots[key]; ok {
			roots[key] = root
		} else if root, ok := state.Roots.Roots[id]; ok && root == tree.SessionRootKey(harnessName, id) {
			roots[key] = root
		}
		if at, ok := state.Roots.Missing[key]; ok {
			missing[key] = at
		} else if at, ok := state.Roots.Missing[id]; ok {
			missing[key] = at
		}
	}
	state.Roots.Roots, state.Roots.Missing = roots, missing
}
func saveState(home string, state *diskState) error {
	dir := filepath.Join(home, "state")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("secure state directory: %w", err)
	}
	body, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode session state: %w", err)
	}
	file, err := os.CreateTemp(dir, ".sessions-*.json")
	if err != nil {
		return fmt.Errorf("create session state: %w", err)
	}
	name := file.Name()
	defer func() { _ = os.Remove(name) }()
	if err = file.Chmod(0o600); err == nil {
		_, err = file.Write(body)
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(name, filepath.Join(dir, "sessions.json"))
	}
	if err != nil {
		return fmt.Errorf("write session state: %w", err)
	}
	return nil
}
