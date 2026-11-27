package harness

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestCoreNeverNamesAHarness(t *testing.T) {
	t.Run("repository", func(t *testing.T) {
		workingDir, err := os.Getwd()
		if err != nil {
			t.Fatalf("get working directory: %v", err)
		}
		root := filepath.Clean(filepath.Join(workingDir, "..", ".."))
		findings, err := coreHarnessReferences(root, Adapters())
		if err != nil {
			t.Fatalf("scan core: %v", err)
		}
		for _, finding := range findings {
			t.Error(finding)
		}
	})

	t.Run("detector", func(t *testing.T) {
		root := t.TempDir()
		writeGoFile(t, root, "internal/harness/acme.go", `package harness
func init() { Register(acmeAdapter{}) }
type acmeAdapter struct{}
func (acmeAdapter) Name() string { return "acme" }
const service = "acme-cli"
`)
		writeGoFile(t, root, "internal/core/core.go", `package core
const harnessService = "acme-cli"
const harnessMetric = "acme.cost.usage"
`)
		writeGoFile(t, root, "internal/core/core_test.go", `package core
const testHarnessName = "acme"
`)
		writeGoFile(t, root, "internal/harness/acme_test.go", `package harness
const adapterTestName = "acme"
`)
		writeGoFile(t, root, "internal/harness/registry_test.go", `package harness
const registryTestName = "acme"
`)
		adapter := registryStub{
			name: "acme",
			mapping: Mapping{
				Resource: map[string][]string{"service.name": {"acme-cli"}},
				Metrics:  NativeMetrics{Cost: "acme.cost.usage"},
			},
		}

		findings, err := coreHarnessReferences(root, []Adapter{adapter})
		if err != nil {
			t.Fatalf("scan fixture: %v", err)
		}
		if len(findings) != 4 {
			t.Fatalf("findings = %v, want core production, metric, and non-adapter test literals", findings)
		}
		joined := strings.Join(findings, "\n")
		for _, expected := range []string{
			"internal/core/core.go",
			"internal/core/core_test.go",
			"internal/harness/registry_test.go",
			"acme-cli",
			"acme.cost.usage",
			"acme",
		} {
			if !strings.Contains(joined, expected) {
				t.Errorf("findings = %v, missing %q", findings, expected)
			}
		}
	})
}

func coreHarnessReferences(root string, registered []Adapter) ([]string, error) {
	forbidden := harnessLiterals(registered)
	files, err := goFiles(root)
	if err != nil {
		return nil, err
	}
	adapterFiles, err := adapterFileSet(root, files)
	if err != nil {
		return nil, err
	}

	var findings []string
	for _, name := range files {
		if adapterFiles[name] || adapterFiles[strings.TrimSuffix(name, "_test.go")+".go"] {
			continue
		}
		fileFindings, err := literalsInFile(root, name, forbidden)
		if err != nil {
			return nil, err
		}
		findings = append(findings, fileFindings...)
	}
	return findings, nil
}

func harnessLiterals(registered []Adapter) map[string]struct{} {
	literals := map[string]struct{}{}
	add := func(value string) {
		if value != "" {
			literals[value] = struct{}{}
		}
	}
	for _, adapter := range registered {
		add(adapter.Name())
		mapping := adapter.Mapping()
		for _, values := range mapping.Resource {
			for _, value := range values {
				add(value)
			}
		}
		add(mapping.Metrics.Cost)
	}
	return literals
}

func goFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".worktrees", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) == ".go" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk Go files: %w", err)
	}
	return files, nil
}

func adapterFileSet(root string, files []string) (map[string]bool, error) {
	result := map[string]bool{}
	harnessDir := filepath.Join(root, "internal", "harness")
	for _, name := range files {
		if filepath.Dir(name) != harnessDir || strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			return nil, fmt.Errorf("parse potential adapter %s: %w", name, err)
		}
		if hasRegistration(parsed) {
			result[name] = true
		}
	}
	return result, nil
}

func hasRegistration(file *ast.File) bool {
	found := false
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Recv != nil || function.Name.Name != "init" {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			identifier, ok := call.Fun.(*ast.Ident)
			if ok && identifier.Name == "Register" {
				found = true
				return false
			}
			return true
		})
	}
	return found
}

func literalsInFile(root, name string, forbidden map[string]struct{}) ([]string, error) {
	position := token.NewFileSet()
	parsed, err := parser.ParseFile(position, name, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("parse core file %s: %w", name, err)
	}

	var findings []string
	ast.Inspect(parsed, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(literal.Value)
		if err != nil {
			return true
		}
		if _, found := forbidden[value]; found {
			relative, err := filepath.Rel(root, name)
			if err != nil {
				relative = name
			}
			findings = append(findings, fmt.Sprintf("%s:%d: core contains harness-specific literal %q", filepath.ToSlash(relative), position.Position(literal.Pos()).Line, value))
		}
		return true
	})
	return findings, nil
}

func writeGoFile(t *testing.T, root, name, contents string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create fixture directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}
