package loop

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Config holds the loop runner configuration.
type Config struct {
	ETConfig      string // path to et config YAML for loop runs
	Epic          string // epic ID filter (e.g., "ET-251")
	MaxIterations int    // safety cap on total iterations
	MaxRetries    int    // per-ticket retry limit
	Cooldown      time.Duration // delay between iterations
	DryRun        bool   // show what would run without executing
	Notify        bool   // send notifications on events
	Template      string // custom prompt template path
}

// Result captures the outcome of a loop run.
type Result struct {
	Completed   []string      // ticket IDs completed
	Failed      []string      // ticket IDs that failed after max retries
	Skipped     []string      // ticket IDs skipped (e.g., epic-level)
	Iterations  int           // total iterations executed
	Duration    time.Duration // total wall time
}

// Runner executes the Ralph Wiggum loop.
type Runner struct {
	cfg     Config
	retries map[string]int // per-ticket attempt counts
	log     []string       // log lines for summary report
}

// NewRunner creates a loop runner with the given config.
func NewRunner(cfg Config) *Runner {
	if cfg.MaxIterations == 0 {
		cfg.MaxIterations = 20
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 3
	}
	if cfg.Cooldown == 0 {
		cfg.Cooldown = 5 * time.Second
	}
	return &Runner{
		cfg:     cfg,
		retries: make(map[string]int),
	}
}

// Run executes the loop until all tickets are done or limits are reached.
func (r *Runner) Run() (*Result, error) {
	start := time.Now()
	result := &Result{}

	if r.cfg.Notify {
		notify("et loop started" + epicSuffix(r.cfg.Epic))
	}

	for i := 0; i < r.cfg.MaxIterations; i++ {
		result.Iterations = i + 1

		// Get next unblocked ticket.
		tickets, err := ReadyTickets(r.cfg.Epic)
		if err != nil {
			r.logf("iteration %d: failed to read tickets: %v", i+1, err)
			break
		}

		// Filter out tickets we've already exhausted retries on.
		ticket := r.pickNext(tickets, result.Failed)
		if ticket == nil {
			r.logf("iteration %d: no more actionable tickets", i+1)
			break
		}

		r.logf("iteration %d: working on %s — %s", i+1, ticket.ID, ticket.Title)

		if r.cfg.DryRun {
			r.logf("  [dry-run] would execute et run for %s", ticket.ID)
			result.Skipped = append(result.Skipped, ticket.ID)
			continue
		}

		// Build prompt and execute.
		prompt, err := BuildPrompt(*ticket, r.cfg.Template)
		if err != nil {
			r.logf("  prompt build failed: %v", err)
			r.retries[ticket.ID]++
			continue
		}

		// Run et with the loop config.
		etErr := r.runET(prompt)
		if etErr != nil {
			r.logf("  et run failed: %v", etErr)
			r.retries[ticket.ID]++
			if r.retries[ticket.ID] >= r.cfg.MaxRetries {
				r.logf("  %s exhausted %d retries — skipping", ticket.ID, r.cfg.MaxRetries)
				result.Failed = append(result.Failed, ticket.ID)
				if r.cfg.Notify {
					notify(fmt.Sprintf("ticket %s failed after %d retries", ticket.ID, r.cfg.MaxRetries))
				}
			}
			time.Sleep(r.cfg.Cooldown)
			continue
		}

		// Verify: go build + go test.
		if verifyErr := r.verify(); verifyErr != nil {
			r.logf("  verification failed: %v", verifyErr)
			r.retries[ticket.ID]++
			if r.retries[ticket.ID] >= r.cfg.MaxRetries {
				r.logf("  %s exhausted %d retries — skipping", ticket.ID, r.cfg.MaxRetries)
				result.Failed = append(result.Failed, ticket.ID)
			}
			time.Sleep(r.cfg.Cooldown)
			continue
		}

		// Commit and close ticket.
		if err := r.commitAndClose(ticket.ID, ticket.Title); err != nil {
			r.logf("  commit/close failed: %v", err)
		} else {
			r.logf("  ✓ %s completed", ticket.ID)
			result.Completed = append(result.Completed, ticket.ID)
		}

		time.Sleep(r.cfg.Cooldown)
	}

	result.Duration = time.Since(start)

	if r.cfg.Notify {
		notify(fmt.Sprintf("et loop finished: %d completed, %d failed, %d iterations in %s",
			len(result.Completed), len(result.Failed), result.Iterations, result.Duration.Round(time.Second)))
	}

	return result, nil
}

// Log returns the accumulated log lines.
func (r *Runner) Log() []string {
	return r.log
}

// pickNext selects the highest-priority ticket that hasn't been exhausted.
func (r *Runner) pickNext(tickets []Ticket, failed []string) *Ticket {
	failSet := make(map[string]bool)
	for _, id := range failed {
		failSet[id] = true
	}
	for i := range tickets {
		t := &tickets[i]
		if failSet[t.ID] {
			continue
		}
		// Skip epic-type tickets (we work on their children).
		if t.Type == "epic" {
			continue
		}
		return t
	}
	return nil
}

// runET shells out to `et run` with the loop config and prompt.
func (r *Runner) runET(prompt string) error {
	args := []string{"run"}
	if r.cfg.ETConfig != "" {
		args = append(args, "--config", r.cfg.ETConfig)
	}
	args = append(args, prompt)

	cmd := exec.Command("et", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("et run: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// verify runs go build and go test.
func (r *Runner) verify() error {
	// go build
	buildOut, err := exec.Command("go", "build", "./...").CombinedOutput()
	if err != nil {
		return fmt.Errorf("go build failed: %s: %w", strings.TrimSpace(string(buildOut)), err)
	}

	// go test
	testOut, err := exec.Command("go", "test", "./...").CombinedOutput()
	if err != nil {
		return fmt.Errorf("go test failed: %s: %w", strings.TrimSpace(string(testOut)), err)
	}

	return nil
}

// commitAndClose creates a git commit and closes the ticket in bd.
func (r *Runner) commitAndClose(id, title string) error {
	// Stage all changes.
	if _, err := exec.Command("git", "add", "-A").CombinedOutput(); err != nil {
		return fmt.Errorf("git add: %w", err)
	}

	// Commit with ticket ID.
	msg := fmt.Sprintf("%s: %s\n\nCompleted by et loop (Ralph Wiggum pattern).", id, title)
	if _, err := exec.Command("git", "commit", "-m", msg).CombinedOutput(); err != nil {
		return fmt.Errorf("git commit: %w", err)
	}

	// Close ticket in bd.
	if err := CloseTicket(id); err != nil {
		r.logf("  warning: ticket closed but bd close failed: %v", err)
	}

	return nil
}

func (r *Runner) logf(format string, args ...interface{}) {
	line := fmt.Sprintf(format, args...)
	r.log = append(r.log, line)
	fmt.Println(line)
}

func epicSuffix(epic string) string {
	if epic != "" {
		return fmt.Sprintf(" (scoped to %s)", epic)
	}
	return ""
}
