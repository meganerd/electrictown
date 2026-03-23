package agent

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// gracePeriod is how long to wait after SIGTERM before sending SIGKILL.
const gracePeriod = 5 * time.Second

// Session manages the lifecycle of a single agent CLI process.
type Session struct {
	// Command is the CLI binary to run (e.g., "claude", "aider").
	Command string

	// Args are the CLI arguments (constructed by the adapter).
	Args []string

	// Dir is the working directory for the process.
	Dir string

	// Env contains additional environment variables (merged with os.Environ).
	Env map[string]string
}

// RunOneShot spawns the agent process, optionally feeds stdin, waits for exit,
// and returns the collected stdout/stderr. The context controls cancellation
// and timeout.
func (s *Session) RunOneShot(ctx context.Context, stdin string) (*Result, error) {
	start := time.Now()

	cmd := exec.CommandContext(ctx, s.Command, s.Args...)

	if s.Dir != "" {
		cmd.Dir = s.Dir
	}

	// Merge environment: current process env + session overrides.
	cmd.Env = buildEnv(s.Env)

	// Set up stdin if provided.
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Use process group so we can kill child processes on timeout.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return &Result{
			Stderr:   fmt.Sprintf("failed to start %s: %s", s.Command, err),
			ExitCode: -1,
			Duration: time.Since(start),
		}, fmt.Errorf("starting %s: %w", s.Command, err)
	}

	// Wait for the process to finish or context to cancel.
	err := cmd.Wait()
	duration := time.Since(start)

	result := &Result{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: duration,
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.ExitCode = -1
		}

		// If context was cancelled/timed out, try graceful shutdown.
		if ctx.Err() != nil {
			s.gracefulShutdown(cmd.Process)
			return result, fmt.Errorf("%s timed out after %s: %w", s.Command, duration, ctx.Err())
		}

		return result, nil // Non-zero exit is not an error — it's in ExitCode.
	}

	return result, nil
}

// gracefulShutdown sends SIGTERM to the process group, waits for gracePeriod,
// then sends SIGKILL if still alive.
func (s *Session) gracefulShutdown(proc *os.Process) {
	if proc == nil {
		return
	}

	// Send SIGTERM to the process group.
	pgid, err := syscall.Getpgid(proc.Pid)
	if err != nil {
		// Process already exited.
		return
	}
	_ = syscall.Kill(-pgid, syscall.SIGTERM)

	// Wait for grace period, then SIGKILL.
	done := make(chan struct{})
	go func() {
		_, _ = proc.Wait()
		close(done)
	}()

	select {
	case <-done:
		return
	case <-time.After(gracePeriod):
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		<-done
	}
}

// buildEnv merges additional env vars into the current process environment.
func buildEnv(extra map[string]string) []string {
	if len(extra) == 0 {
		return nil // nil means inherit parent env.
	}
	env := os.Environ()
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}
