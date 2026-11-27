package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/user"
	"sort"
	"sync"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.uber.org/zap"
)

type Extension struct {
	registry *Registry
	config   *Config
	settings component.TelemetrySettings
	mu       sync.Mutex
	listener net.Listener
	server   *http.Server
}

func (e *Extension) Start(_ context.Context, _ component.Host) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.config.Endpoint == "" {
		return nil
	}
	listener, err := net.Listen("tcp", e.config.Endpoint)
	if err != nil {
		return fmt.Errorf("listen for status: %w", err)
	}
	server := &http.Server{Handler: http.HandlerFunc(e.serveHTTP), ReadHeaderTimeout: 5 * time.Second}
	e.listener = listener
	e.server = server
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			e.settings.Logger.Error("status server stopped", zap.Error(err))
		}
	}()
	return nil
}
func (e *Extension) Shutdown(ctx context.Context) error {
	e.mu.Lock()
	server := e.server
	e.server = nil
	e.listener = nil
	e.mu.Unlock()
	if server != nil {
		return server.Shutdown(ctx)
	}
	return nil
}
func (e *Extension) serveHTTP(w http.ResponseWriter, req *http.Request) {
	if req.URL.Path != "/status" {
		http.NotFound(w, req)
		return
	}
	if req.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(e.registry.status(e.config))
}

type harnessStatus struct {
	Name           string     `json:"name"`
	Runs           int        `json:"runs"`
	NativeLastSeen *time.Time `json:"native_last_seen"`
}
type droppedStatus struct {
	Key   string `json:"key"`
	Count uint64 `json:"count"`
}
type status struct {
	Version      string          `json:"version"`
	Mode         string          `json:"mode"`
	PID          int             `json:"pid"`
	User         string          `json:"user"`
	Uptime       int64           `json:"uptime_s"`
	HashKeyID    string          `json:"hash_key_id"`
	HashMismatch uint64          `json:"hash_key_mismatch"`
	Harnesses    []harnessStatus `json:"harnesses"`
	Unresolved   uint64          `json:"unresolved"`
	Dropped      []droppedStatus `json:"dropped"`
}

func (r *Registry) status(config *Config) status {
	r.mu.RLock()
	defer r.mu.RUnlock()
	byHarness := map[string]*harnessStatus{}
	for key := range r.snaps {
		h := byHarness[key.Harness]
		if h == nil {
			h = &harnessStatus{Name: key.Harness}
			byHarness[key.Harness] = h
		}
		h.Runs++
	}
	for key, at := range r.nativeLast {
		h := byHarness[key.Harness]
		if h == nil {
			h = &harnessStatus{Name: key.Harness}
			byHarness[key.Harness] = h
		}
		if h.NativeLastSeen == nil || at.After(*h.NativeLastSeen) {
			copy := at
			h.NativeLastSeen = &copy
		}
	}
	name := ""
	if current, err := user.Current(); err == nil {
		name = current.Username
	}
	out := status{Version: "0.1.0-dev", Mode: config.Mode, PID: os.Getpid(), User: name, Uptime: int64(time.Since(r.started).Seconds()), HashKeyID: config.HashKeyID, HashMismatch: r.hashMismatch, Unresolved: r.unresolved, Harnesses: []harnessStatus{}, Dropped: []droppedStatus{}}
	for _, h := range byHarness {
		out.Harnesses = append(out.Harnesses, *h)
	}
	sort.Slice(out.Harnesses, func(i, j int) bool { return out.Harnesses[i].Name < out.Harnesses[j].Name })
	for key, count := range r.dropped {
		out.Dropped = append(out.Dropped, droppedStatus{key, count})
	}
	sort.Slice(out.Dropped, func(i, j int) bool { return out.Dropped[i].Key < out.Dropped[j].Key })
	return out
}
