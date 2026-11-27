package ember

import (
	"fmt"

	"go.opentelemetry.io/collector/component"

	"github.com/crucibledx/ember/internal/privacy"
)

type Config struct {
	Registry component.ID  `mapstructure:"registry"`
	Privacy  PrivacyConfig `mapstructure:"privacy"`
}

type PrivacyConfig struct {
	Content          bool                  `mapstructure:"content"`
	Allow            []string              `mapstructure:"allow"`
	Deny             []string              `mapstructure:"deny"`
	HumanIdentity    privacy.HumanIdentity `mapstructure:"human_identity"`
	HashKey          string                `mapstructure:"hash_key"`
	HashKeyID        string                `mapstructure:"hash_key_id"`
	RestoreToolNames bool                  `mapstructure:"restore_tool_names"`
}

func createDefaultConfig() component.Config {
	return &Config{Registry: component.MustNewID("ember"), Privacy: PrivacyConfig{
		HumanIdentity: privacy.HumanHash, RestoreToolNames: true,
	}}
}

func (c *Config) Validate() error {
	if c.Registry.String() == "" {
		return fmt.Errorf("registry must name the Ember registry extension")
	}
	_, err := privacy.New(privacy.Config{
		Content: c.Privacy.Content, Allow: c.Privacy.Allow, Deny: c.Privacy.Deny,
		HumanIdentity: c.Privacy.HumanIdentity, HashKey: c.Privacy.HashKey,
	})
	return err
}
