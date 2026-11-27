package registry

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/extension"

	"github.com/crucibledx/ember/internal/extension/registry/internal/metadata"
)

func NewFactory() extension.Factory {
	return extension.NewFactory(metadata.Type, createDefaultConfig, createExtension, metadata.ExtensionStability)
}
func createExtension(_ context.Context, set extension.Settings, cfg component.Config) (extension.Extension, error) {
	return &Extension{registry: New(), config: cfg.(*Config), settings: set.TelemetrySettings}, nil
}
