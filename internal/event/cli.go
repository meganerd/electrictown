package event

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// CLISubscriber renders events as human-readable console output,
// reproducing the original fmt.Printf output from main.go.
type CLISubscriber struct {
	out io.Writer
	err io.Writer
}

// NewCLISubscriber creates a subscriber that writes to the given writers.
// If out is nil, os.Stdout is used. If errw is nil, os.Stderr is used.
func NewCLISubscriber(out, errw io.Writer) *CLISubscriber {
	if out == nil {
		out = os.Stdout
	}
	if errw == nil {
		errw = os.Stderr
	}
	return &CLISubscriber{out: out, err: errw}
}

// Run consumes events from the subscriber channel until it is closed.
// Intended to be called in a goroutine.
func (c *CLISubscriber) Run(sub Subscriber) {
	for e := range sub {
		c.handle(e)
	}
}

func (c *CLISubscriber) handle(e Event) {
	switch e.Type {
	case TaskStarted:
		if d, ok := e.Data.(TaskStartedData); ok {
			fmt.Fprintf(c.out, "electrictown %s\n", d.Version)
			fmt.Fprintf(c.out, "============\n")
			fmt.Fprintf(c.out, "Config: %s\n", d.ConfigPath)
			fmt.Fprintf(c.out, "Task:   %s\n", d.Task)
			fmt.Fprintf(c.out, "Logs:   %s\n", d.LogDir)
			fmt.Fprintf(c.out, "Start:  %s\n\n", e.Timestamp.Format("15:04:05"))
		}

	case PhaseStarted:
		if d, ok := e.Data.(PhaseData); ok {
			if d.Detail != "" {
				fmt.Fprintf(c.out, "%s: %s\n", d.Name, d.Detail)
			} else {
				fmt.Fprintf(c.out, "%s\n", d.Name)
			}
		}

	case PhaseCompleted:
		fmt.Fprintln(c.out)

	case SubtaskDecomposed:
		if d, ok := e.Data.(SubtaskDecomposedData); ok {
			fmt.Fprintf(c.out, "  Subtasks: %d\n", len(d.Subtasks))
			for i, st := range d.Subtasks {
				fmt.Fprintf(c.out, "  [%d] %s\n", i+1, truncate(st, 100))
			}
			if d.HasDeps {
				fmt.Fprintf(c.out, "  Dependencies detected — will execute in waves\n")
			}
		}

	case WorkerStarted:
		// No-op for CLI — the progress hook handles this.

	case WorkerProgress:
		// No-op for CLI — live progress handles this.

	case WorkerCompleted:
		if d, ok := e.Data.(WorkerData); ok {
			status := "✓"
			if d.Flagged {
				status = "⚑"
			}
			toks := fmt.Sprintf("%d tok", d.Tokens)
			tps := ""
			if d.Duration > 0 && d.Tokens > 0 {
				tps = fmt.Sprintf(", %.0f tok/s", float64(d.Tokens)/d.Duration.Seconds())
			}
			fmt.Fprintf(c.out, "  [%d/%d] %-18s %s (%s%s, %.1fs)\n",
				d.Index+1, d.Total, truncate(d.Model, 18), status, toks, tps, d.Duration.Seconds())
		}

	case WorkerFailed:
		if d, ok := e.Data.(WorkerData); ok {
			fmt.Fprintf(c.err, "  worker[%d]: %s\n", d.Index+1, d.Error)
		}

	case DAGWaveStarted:
		if d, ok := e.Data.(DAGWaveData); ok {
			fmt.Fprintf(c.out, "  Wave %d: %d subtask(s)\n", d.Wave, len(d.Indices))
		}

	case DAGWaveCompleted:
		// No-op for CLI.

	case GuardrailRetry:
		if d, ok := e.Data.(GuardrailRetryData); ok {
			fmt.Fprintf(c.out, "  [%d] score=%d/10 ⚑ retrying (%d/%d): %s\n",
				d.Index+1, d.Score, d.Attempt, d.MaxRetries, truncate(d.Note, 60))
		}

	case SpecialistAssigned:
		if d, ok := e.Data.(SpecialistAssignedData); ok {
			if d.Specialist == "" {
				fmt.Fprintf(c.out, "  [%d] → general-default\n", d.Index+1)
			} else {
				fmt.Fprintf(c.out, "  [%d] → %s (%s)\n", d.Index+1, d.Specialist, d.Model)
			}
		}

	case CostUpdate:
		// No-op for inline CLI — shown in summary at end.

	case RAGIngest:
		if d, ok := e.Data.(RAGData); ok {
			fmt.Fprintf(c.out, "  Auto-ingested %d chunks\n", d.Count)
		}

	case RAGQuery:
		if d, ok := e.Data.(RAGData); ok {
			fmt.Fprintf(c.out, "  Retrieved %d chunks\n", d.Count)
		}

	case JinaFetch:
		if d, ok := e.Data.(JinaFetchData); ok {
			if d.Error != "" {
				fmt.Fprintf(c.err, "  warning: Jina fetch %s: %s\n", d.URL, d.Error)
			} else {
				fmt.Fprintf(c.out, "  ✓ fetched %s (%d chars)\n", d.URL, d.Length)
			}
		}

	case ValidationResult:
		if d, ok := e.Data.(ValidationData); ok {
			if !d.Passed {
				fmt.Fprintf(c.out, "  ⚠ worker[%d] output validation failed: %s\n",
					d.Index+1, strings.Join(d.Errors, "; "))
			} else {
				fmt.Fprintf(c.out, "  ✓ worker[%d] re-submitted after validation fix\n", d.Index+1)
			}
		}

	case RunCompleted:
		if d, ok := e.Data.(RunCompletedData); ok {
			fmt.Fprintf(c.out, "\nDone: run completed in %s\n", d.Duration.Round(time.Millisecond))
		}
	}
}

// truncate shortens s to max characters with trailing "…" if needed.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	return s[:max-1] + "…"
}

// Compile-time check that time is used.
var _ time.Duration
