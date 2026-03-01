// Package build provides language detection and build command execution
// for the Phase 5 iterative build/test loop.
package build

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Runner executes a build command in a directory and reports results.
type Runner interface {
	// Run executes the build in dir. Returns captured stdout, stderr,
	// and a non-nil error if the build failed (non-zero exit or exec error).
	Run(ctx context.Context, dir string) (stdout, stderr string, err error)
	// Name returns a human-readable label for the runner (e.g. "go", "node").
	Name() string
}

// DetectRunner inspects dir and returns an appropriate Runner, or nil if no
// supported build system is found.
func DetectRunner(dir string) Runner {
	if fileExists(filepath.Join(dir, "go.mod")) {
		return &GoRunner{}
	}
	if fileExists(filepath.Join(dir, "package.json")) {
		return &NodeRunner{}
	}
	if fileExists(filepath.Join(dir, "Makefile")) || fileExists(filepath.Join(dir, "makefile")) {
		return &MakeRunner{}
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func runCmd(ctx context.Context, dir string, name string, args ...string) (stdout, stderr string, err error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	runErr := cmd.Run()
	stdout = outBuf.String()
	stderr = errBuf.String()

	if runErr != nil {
		err = fmt.Errorf("%s: %w", name, runErr)
	}
	return stdout, stderr, err
}

// GoRunner builds a Go module with "go build ./...".
type GoRunner struct{}

func (r *GoRunner) Name() string { return "go" }

func (r *GoRunner) Run(ctx context.Context, dir string) (string, string, error) {
	return runCmd(ctx, dir, "go", "build", "./...")
}

// NodeRunner builds a Node.js project. Prefers bun if available, falls back to npm.
type NodeRunner struct{}

func (r *NodeRunner) Name() string { return "node" }

func (r *NodeRunner) Run(ctx context.Context, dir string) (string, string, error) {
	// Prefer bun if available.
	if _, err := exec.LookPath("bun"); err == nil {
		return runCmd(ctx, dir, "bun", "run", "build")
	}
	return runCmd(ctx, dir, "npm", "run", "build")
}

// MakeRunner runs the default make target.
type MakeRunner struct{}

func (r *MakeRunner) Name() string { return "make" }

func (r *MakeRunner) Run(ctx context.Context, dir string) (string, string, error) {
	return runCmd(ctx, dir, "make")
}
