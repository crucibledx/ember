//go:build e2e

// Package harness builds the ember binary for the end-to-end tests and runs it with an isolated home directory.
package harness

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

const mainPackage = "github.com/crucibledx/ember/cmd/ember"

// Binary is an ember binary built for one test package.
type Binary struct {
	path string
	err  error
}

// Result is what one run of the binary printed and returned.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Build compiles ember into a new temporary directory and returns a cleanup that removes it. A build failure is
// reported by every Run instead of being returned, so TestMain always reaches m.Run and the cleanup.
func Build() (*Binary, func()) {
	dir, err := os.MkdirTemp("", "ember-e2e-")
	if err != nil {
		return &Binary{err: fmt.Errorf("create build directory: %w", err)}, func() {}
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	name := "ember"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(dir, name)
	out, err := exec.Command("go", "build", "-o", path, mainPackage).CombinedOutput()
	if err != nil {
		return &Binary{err: fmt.Errorf("go build %s: %w\n%s", mainPackage, err, out)}, cleanup
	}
	return &Binary{path: path}, cleanup
}

// Run runs the binary with args in home, which is also its HOME and working directory, and returns its output and
// exit code. Nothing from the caller's environment reaches it except PATH (and SystemRoot on Windows).
func (b *Binary) Run(t testing.TB, home string, args ...string) Result {
	t.Helper()
	if b.err != nil {
		t.Fatalf("build ember: %v", b.err)
	}
	cmd := exec.CommandContext(t.Context(), b.path, args...)
	cmd.Dir = home
	cmd.Env = isolatedEnv(home)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	code := 0
	var exit *exec.ExitError
	switch err := cmd.Run(); {
	case errors.As(err, &exit):
		code = exit.ExitCode()
	case err != nil:
		t.Fatalf("run ember %v: %v", args, err)
	}
	return Result{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: code}
}

func isolatedEnv(home string) []string {
	env := []string{
		"HOME=" + home,
		"USERPROFILE=" + home,
		"APPDATA=" + filepath.Join(home, "AppData", "Roaming"),
		"LOCALAPPDATA=" + filepath.Join(home, "AppData", "Local"),
		"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
		"PATH=" + os.Getenv("PATH"),
	}
	if runtime.GOOS == "windows" {
		env = append(env, "SystemRoot="+os.Getenv("SystemRoot"))
	}
	return env
}
