package session

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// cancelFuncs stores context cancel functions keyed by session ID.
// This is package-level because SessionLauncher's struct definition lives
// in session.go and we avoid modifying it. Access is guarded by cancelMu.
var (
	cancelFuncs = make(map[string]context.CancelFunc)
	cancelMu    sync.Mutex
)

// storeCancelFunc saves a cancel function for the given session ID.
func storeCancelFunc(sessionID string, cancel context.CancelFunc) {
	cancelMu.Lock()
	defer cancelMu.Unlock()
	cancelFuncs[sessionID] = cancel
}

// removeCancelFunc removes and returns the cancel function for the given session ID.
func removeCancelFunc(sessionID string) (context.CancelFunc, bool) {
	cancelMu.Lock()
	defer cancelMu.Unlock()
	cancel, ok := cancelFuncs[sessionID]
	if ok {
		delete(cancelFuncs, sessionID)
	}
	return cancel, ok
}

// Execute launches the agent session as a subprocess, captures output, and manages lifecycle.
// It runs the command built by the adapter, streams stdout/stderr to Session.Output,
// and transitions status through starting->running->done (or failed).
func (l *SessionLauncher) Execute(ctx context.Context, sess *Session) error {
	// Build the command from the adapter.
	cmdName, args := l.adapter.BuildCommand(sess.Config, sess.Prompt)

	// Transition to starting.
	sess.SetStatus(StatusStarting)

	// Create a cancellable context, optionally with timeout.
	var execCtx context.Context
	var cancel context.CancelFunc

	if sess.Config.Timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, sess.Config.Timeout)
	} else {
		execCtx, cancel = context.WithCancel(ctx)
	}

	// Store cancel func so Stop() can use it.
	storeCancelFunc(sess.ID, cancel)
	defer func() {
		removeCancelFunc(sess.ID)
		cancel()
	}()

	// Build the exec.Cmd.
	cmd := exec.CommandContext(execCtx, cmdName, args...)

	// Set working directory.
	if sess.Config.WorkDir != "" {
		cmd.Dir = sess.Config.WorkDir
	}

	// Merge environment: start with current process env, overlay session config.
	env := os.Environ()
	for k, v := range sess.Config.Env {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	cmd.Env = env

	// Pipe stdout and stderr to session output.
	sess.mu.Lock()
	writer := io.Writer(&sess.Output)
	sess.mu.Unlock()

	cmd.Stdout = writer
	cmd.Stderr = writer

	// Start the process.
	if err := cmd.Start(); err != nil {
		sess.SetStatus(StatusFailed)
		return fmt.Errorf("start command %q: %w", cmdName, err)
	}

	// Transition to running.
	sess.SetStatus(StatusRunning)
	sess.mu.Lock()
	sess.StartedAt = time.Now()
	sess.mu.Unlock()

	// Wait for completion.
	err := cmd.Wait()
	if err != nil {
		sess.SetStatus(StatusFailed)
		// Check if context was cancelled or timed out.
		if execCtx.Err() != nil {
			return fmt.Errorf("session %s: %w", sess.ID, execCtx.Err())
		}
		return fmt.Errorf("session %s command failed: %w", sess.ID, err)
	}

	sess.SetStatus(StatusDone)
	return nil
}

// ExecuteAsync launches the session in a background goroutine.
// Returns immediately. Check session Status for progress.
func (l *SessionLauncher) ExecuteAsync(ctx context.Context, sess *Session) {
	go func() {
		_ = l.Execute(ctx, sess)
	}()
}

// Stop terminates a running session by cancelling its context.
func (l *SessionLauncher) Stop(sessionID string) error {
	cancel, ok := removeCancelFunc(sessionID)
	if !ok {
		return fmt.Errorf("no running session with ID %q", sessionID)
	}
	cancel()
	return nil
}
