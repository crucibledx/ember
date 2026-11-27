package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"slices"
	"sort"
	"testing"

	"go.opentelemetry.io/collector/component"

	"github.com/crucibledx/ember/internal/buildinfo"
	"github.com/crucibledx/ember/internal/cli"
)

// renamedFactory is implemented by factories that upstream renamed and that still accept their old type name.
type renamedFactory interface{ DeprecatedAlias() component.Type }

// acceptedNames lists, per factory, the type names a collector config may use for it.
func acceptedNames[F component.Factory](factories map[component.Type]F) [][]string {
	var out [][]string
	for typ, factory := range factories {
		names := []string{typ.String()}
		if ap, ok := any(factory).(renamedFactory); ok && ap.DeprecatedAlias().String() != "" {
			names = append(names, ap.DeprecatedAlias().String())
		}
		sort.Strings(names)
		out = append(out, names)
	}
	return out
}

func TestComponentsMatchSpec(t *testing.T) {
	// Spec 0001 §1, by the names it uses. Upstream renamed some exporters (otlp → otlp_grpc, otlphttp → otlp_http)
	// and keeps the old names as aliases, so a factory matches a spec name by its type or its alias.
	spec := map[string][]string{
		"receiver":  {"ember_sessions", "otlp"},
		"processor": {"batch", "deltatocumulative", "ember", "memory_limiter"},
		"exporter":  {"debug", "otlp", "otlphttp"},
		"extension": {"bearertokenauth", "ember"},
		"connector": {},
	}
	// Ember's own components land with the issue that implements them; that issue deletes its entry here.
	notYetBuilt := map[string]string{
		"receiver/ember_sessions": "crucibledx/ember#9",
		"processor/ember":         "crucibledx/ember#9",
		"extension/ember":         "crucibledx/ember#9",
	}

	factories, err := components()
	if err != nil {
		t.Fatalf("components: %v", err)
	}
	built := map[string][][]string{
		"receiver":  acceptedNames(factories.Receivers),
		"processor": acceptedNames(factories.Processors),
		"exporter":  acceptedNames(factories.Exporters),
		"extension": acceptedNames(factories.Extensions),
		"connector": acceptedNames(factories.Connectors),
	}

	for kind, names := range spec {
		for _, accepted := range built[kind] {
			if !slices.ContainsFunc(accepted, func(n string) bool { return slices.Contains(names, n) }) {
				t.Errorf("%s %v is built but not in spec §1", kind, accepted)
			}
		}
		for _, name := range names {
			isBuilt := slices.ContainsFunc(built[kind], func(accepted []string) bool { return slices.Contains(accepted, name) })
			issue, pending := notYetBuilt[kind+"/"+name]
			switch {
			case pending && isBuilt:
				t.Errorf("%s %q is built now; delete its notYetBuilt entry (%s)", kind, name, issue)
			case !pending && !isBuilt:
				t.Errorf("%s %q from spec §1 is missing", kind, name)
			}
		}
	}
}

func TestRunValidatesCollectorConfig(t *testing.T) {
	config := filepath.Join(t.TempDir(), "collector.yaml")
	body := []byte(`receivers:
  otlp:
    protocols:
      http:
        endpoint: ${EMBER_TEST_ENDPOINT}
processors:
  batch:
exporters:
  debug:
service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [batch]
      exporters: [debug]
`)
	if err := os.WriteFile(config, body, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EMBER_TEST_ENDPOINT", "127.0.0.1:4318")

	info := buildinfo.New("0.1.0", "", "", nil)
	root := cli.NewRootCommand(info, collectorSettings(info))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"run", "validate", "--config", config})
	if err := root.Execute(); err != nil {
		t.Fatalf("ember run validate: %v\n%s", err, out.String())
	}
}

// The otelcol version comes from the module graph of the binary that links the collector, so it is checked here.
func TestBuildInfoReadsLinkedCollector(t *testing.T) {
	build, ok := debug.ReadBuildInfo()
	if !ok {
		t.Fatal("no build information in the test binary")
	}
	got := buildinfo.New("0.1.0", "", "", build).OtelCol
	if !regexp.MustCompile(`^0\.\d+\.\d+$`).MatchString(got) {
		t.Errorf("otelcol version = %q, want the linked otelcol module version", got)
	}
}
