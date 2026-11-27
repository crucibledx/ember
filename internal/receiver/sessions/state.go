package sessions

import (
	"encoding/json"
	"errors"
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
	Files        map[string]fileState `json:"files"`
	Run          harness.Run          `json:"run"`
	LastSegment  int                  `json:"last_segment"`
	Tools        []string             `json:"tools"`
	LogTime      time.Time            `json:"log_time"`
	LogOrdinal   int                  `json:"log_ordinal"`
	FinalMetrics bool                 `json:"final_metrics"`
}
type diskState struct {
	Version int                  `json:"version"`
	Runs    map[string]*runState `json:"runs"`
	Roots   tree.RootPins        `json:"roots"`
	Native  map[string]time.Time `json:"native_first_seen"`
}

func loadState(home string) *diskState {
	s := &diskState{Version: 1, Runs: map[string]*runState{}, Roots: tree.RootPins{Roots: map[string]string{}, Missing: map[string]time.Time{}}, Native: map[string]time.Time{}}
	body, err := os.ReadFile(filepath.Join(home, "state", "sessions.json"))
	if err != nil {
		return s
	}
	var loaded diskState
	if json.Unmarshal(body, &loaded) != nil || loaded.Version != 1 || loaded.Runs == nil {
		return s
	}
	if loaded.Roots.Roots == nil {
		loaded.Roots.Roots = map[string]string{}
	}
	if loaded.Roots.Missing == nil {
		loaded.Roots.Missing = map[string]time.Time{}
	}
	if loaded.Native == nil {
		loaded.Native = map[string]time.Time{}
	}
	return &loaded
}
func saveState(home string, state *diskState) error {
	dir := filepath.Join(home, "state")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	body, err := json.Marshal(state)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".sessions-*.json")
	if err != nil {
		return err
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
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
