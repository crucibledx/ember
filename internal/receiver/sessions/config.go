package sessions

import (
	"fmt"
	"time"

	"go.opentelemetry.io/collector/component"
)

type Config struct {
	Home        string        `mapstructure:"home"`
	Poll        time.Duration `mapstructure:"poll"`
	Lookback    time.Duration `mapstructure:"lookback"`
	Idle        time.Duration `mapstructure:"idle"`
	NativeGrace time.Duration `mapstructure:"native_grace"`
	Rescan      time.Duration `mapstructure:"rescan"`
	Registry    component.ID  `mapstructure:"registry"`
}

func createDefaultConfig() component.Config {
	return &Config{Poll: 5 * time.Second, Lookback: 168 * time.Hour, Idle: 30 * time.Minute, NativeGrace: 2 * time.Minute, Registry: component.MustNewID("ember")}
}
func (c *Config) Validate() error {
	for name, value := range map[string]time.Duration{"poll": c.Poll, "lookback": c.Lookback, "idle": c.Idle, "native_grace": c.NativeGrace, "rescan": c.Rescan} {
		if value < 0 {
			return fmt.Errorf("%s must not be negative", name)
		}
	}
	if c.Poll == 0 {
		return fmt.Errorf("poll must be greater than zero")
	}
	if c.Lookback == 0 {
		return fmt.Errorf("lookback must be greater than zero")
	}
	if c.Idle == 0 {
		return fmt.Errorf("idle must be greater than zero")
	}
	return nil
}
