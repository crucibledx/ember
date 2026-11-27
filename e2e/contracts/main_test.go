//go:build e2e

package contracts_test

import (
	"testing"

	"github.com/crucibledx/ember/e2e/harness"
)

// ember is set once by TestMain, before any test runs.
var ember *harness.Binary

func TestMain(m *testing.M) {
	bin, cleanup := harness.Build()
	defer cleanup()
	ember = bin
	m.Run()
}
