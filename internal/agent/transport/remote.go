package transport

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/meganerd/electrictown/internal/agent"
)

// RemoteSession manages a single agent execution on a remote host via SSH.
type RemoteSession struct {
	Client  *ssh.Client
	Manager *SSHManager
}

// RunRemote spawns an agent command on the remote host, pipes stdin,
// and collects stdout/stderr. The context controls timeout/cancellation.
func (rs *RemoteSession) RunRemote(ctx context.Context, command string, args []string, opts RemoteOpts) (*agent.Result, error) {
	start := time.Now()

	session, err := rs.Manager.NewSession(rs.Client)
	if err != nil {
		return &agent.Result{
			ExitCode: -1,
			Duration: time.Since(start),
			Stderr:   fmt.Sprintf("failed to create SSH session: %s", err),
		}, err
	}
	defer session.Close()

	// Set environment variables on remote session.
	for k, v := range opts.Env {
		// session.Setenv may fail if server doesn't accept it —
		// fall back to prepending export commands.
		if err := session.Setenv(k, v); err != nil {
			// Silently ignore — we'll use shell env prefix instead.
		}
	}

	// Build the full command string.
	cmdStr := buildRemoteCommand(command, args, opts)

	// Set up stdin.
	if opts.Stdin != "" {
		session.Stdin = strings.NewReader(opts.Stdin)
	}

	// Capture stdout/stderr.
	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	// Run with context cancellation support.
	done := make(chan error, 1)
	go func() {
		done <- session.Run(cmdStr)
	}()

	var runErr error
	select {
	case runErr = <-done:
		// Process completed.
	case <-ctx.Done():
		// Context cancelled/timed out — signal the session to close,
		// which kills the remote process.
		_ = session.Signal(ssh.SIGTERM)
		// Give it a moment, then force close.
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			session.Close()
			<-done
		}
		runErr = ctx.Err()
	}

	duration := time.Since(start)
	result := &agent.Result{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: duration,
	}

	if runErr != nil {
		if exitErr, ok := runErr.(*ssh.ExitError); ok {
			result.ExitCode = exitErr.ExitStatus()
		} else if ctx.Err() != nil {
			result.ExitCode = -1
			return result, fmt.Errorf("remote command timed out after %s: %w", duration, ctx.Err())
		} else {
			result.ExitCode = -1
		}
	}

	return result, nil
}

// RemoteOpts configures a remote execution.
type RemoteOpts struct {
	WorkingDir string
	Env        map[string]string
	Stdin      string
}

// buildRemoteCommand constructs the shell command string for remote execution.
// It handles working directory, environment, and argument quoting.
func buildRemoteCommand(command string, args []string, opts RemoteOpts) string {
	var parts []string

	// Change to working directory if specified.
	if opts.WorkingDir != "" {
		parts = append(parts, fmt.Sprintf("cd %s &&", shellQuote(opts.WorkingDir)))
	}

	// Prepend environment variables (more reliable than session.Setenv).
	for k, v := range opts.Env {
		parts = append(parts, fmt.Sprintf("export %s=%s &&", k, shellQuote(v)))
	}

	// The command itself.
	parts = append(parts, command)
	for _, arg := range args {
		parts = append(parts, shellQuote(arg))
	}

	return strings.Join(parts, " ")
}

// shellQuote wraps a string in single quotes for safe shell interpolation.
func shellQuote(s string) string {
	// Replace single quotes with '\'' (end quote, escaped quote, start quote).
	escaped := strings.ReplaceAll(s, "'", "'\\''")
	return "'" + escaped + "'"
}
