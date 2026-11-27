package registry

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/extension"
)

func TestStatusUsesBuildVersion(t *testing.T) {
	instance, err := createExtension(context.Background(), extension.Settings{
		BuildInfo: component.BuildInfo{Version: "2.3.4"},
	}, createDefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	instance.(*Extension).serveHTTP(response, httptest.NewRequest("GET", "/status", nil))
	var got status
	if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Version != "2.3.4" {
		t.Fatalf("version=%q", got.Version)
	}
}
