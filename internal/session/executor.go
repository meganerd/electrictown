package session

import "context"

// Executor abstracts how agent sessions are launched and stopped.
// SubprocessExecutor runs sessions as child processes (used by et run).
// TmuxExecutor runs sessions in tmux panes (used by et session spawn).
type Executor interface {
	// Execute launches the session. Blocking behavior depends on implementation:
	// SubprocessExecutor blocks until the process exits.
	// TmuxExecutor returns after session creation (non-blocking).
	Execute(ctx context.Context, sess *Session) error

	// Stop terminates a running session by its ID.
	Stop(sessionID string) error
}
