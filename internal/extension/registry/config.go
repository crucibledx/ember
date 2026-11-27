package registry

import (
	"fmt"

	"go.opentelemetry.io/collector/component"
)

type Config struct {
	Endpoint  string `mapstructure:"endpoint"`
	Mode      string `mapstructure:"mode"`
	HashKeyID string `mapstructure:"hash_key_id"`
}

func createDefaultConfig() component.Config { return &Config{Mode: "agent"} }
func (c *Config) Validate() error {
	if c.Mode != "agent" && c.Mode != "gateway" {
		return fmt.Errorf("mode must be agent or gateway")
	}
	return nil
}
