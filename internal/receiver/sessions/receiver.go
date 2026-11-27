package sessions

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.uber.org/zap"

	"github.com/crucibledx/ember/internal/extension/registry"
)

type sessionReceiver struct {
	config  *Config
	traces  consumer.Traces
	metrics consumer.Metrics
	logs    consumer.Logs
	core    *core
	mu      sync.Mutex
	started int
	cancel  context.CancelFunc
	done    chan struct{}
}

func newSessionReceiver(config *Config, logger *zap.Logger) *sessionReceiver {
	core := newCore(config, nil)
	core.log = logger
	return &sessionReceiver{config: config, core: core}
}
func (r *sessionReceiver) Start(ctx context.Context, host component.Host) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.core.traces, r.core.metrics, r.core.logs = r.traces, r.metrics, r.logs
	if r.started > 0 {
		r.started++
		return nil
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	r.core.userHome = userHome
	if r.config.Home == "" {
		r.config.Home = filepath.Join(userHome, ".ember")
	}
	reg, err := registry.FromHost(host, r.config.Registry)
	if err != nil {
		return err
	}
	r.core.registry = reg
	if err = r.core.start(r.config.Home); err != nil {
		return err
	}
	reg.SetSource(r.core)
	pollCtx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.done = make(chan struct{})
	r.started = 1
	r.reportPoll(r.core.Poll(pollCtx))
	go r.loop(pollCtx)
	return nil
}
func (r *sessionReceiver) loop(ctx context.Context) {
	defer close(r.done)
	ticker := time.NewTicker(r.config.Poll)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			r.reportPoll(r.core.Poll(ctx))
		case <-ctx.Done():
			return
		}
	}
}
func (r *sessionReceiver) reportPoll(err error) {
	if err != nil && !errors.Is(err, context.Canceled) {
		r.core.logger().Error("session poll failed", zap.Error(err))
	}
}
func (r *sessionReceiver) Shutdown(ctx context.Context) error {
	r.mu.Lock()
	if r.started == 0 {
		r.mu.Unlock()
		return nil
	}
	r.started--
	if r.started > 0 {
		r.mu.Unlock()
		return nil
	}
	cancel, done := r.cancel, r.done
	r.core.registry.SetSource(nil)
	r.mu.Unlock()
	cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
