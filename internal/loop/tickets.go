// Package loop implements the Ralph Wiggum unattended task runner.
// It reads tickets from bd (beads), executes them via et run,
// verifies with go build/test, and commits on success.
package loop

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// Ticket represents a bd issue parsed from JSON output.
type Ticket struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Acceptance  string `json:"acceptance_criteria"`
	Priority    int    `json:"priority"`
	Type        string `json:"type"`
	Status      string `json:"status"`
}

// ReadyTickets calls `bd ready --json` and returns unblocked tickets.
// If epicFilter is non-empty, only tickets belonging to that epic are returned.
func ReadyTickets(epicFilter string) ([]Ticket, error) {
	args := []string{"ready", "--json"}
	out, err := exec.Command("bd", args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("bd ready: %s: %w", strings.TrimSpace(string(out)), err)
	}

	var tickets []Ticket
	if err := json.Unmarshal(out, &tickets); err != nil {
		return nil, fmt.Errorf("parsing bd ready output: %w", err)
	}

	if epicFilter == "" {
		return tickets, nil
	}

	// Filter by epic: get the epic's blocked list and intersect.
	epicTickets, err := epicChildIDs(epicFilter)
	if err != nil {
		return nil, err
	}

	var filtered []Ticket
	for _, t := range tickets {
		if epicTickets[t.ID] {
			filtered = append(filtered, t)
		}
	}
	return filtered, nil
}

// ShowTicket calls `bd show --json <id>` and returns the full ticket.
func ShowTicket(id string) (*Ticket, error) {
	out, err := exec.Command("bd", "show", "--json", id).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("bd show %s: %s: %w", id, strings.TrimSpace(string(out)), err)
	}

	var ticket Ticket
	if err := json.Unmarshal(out, &ticket); err != nil {
		return nil, fmt.Errorf("parsing bd show output: %w", err)
	}
	return &ticket, nil
}

// CloseTicket calls `bd close <id>`.
func CloseTicket(id string) error {
	out, err := exec.Command("bd", "close", id).CombinedOutput()
	if err != nil {
		return fmt.Errorf("bd close %s: %s: %w", id, strings.TrimSpace(string(out)), err)
	}
	return nil
}

// epicChildIDs returns a set of ticket IDs that are children of the given epic.
// It parses `bd show --json <epicID>` and extracts blocked ticket IDs.
func epicChildIDs(epicID string) (map[string]bool, error) {
	out, err := exec.Command("bd", "show", "--json", epicID).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("bd show %s: %s: %w", epicID, strings.TrimSpace(string(out)), err)
	}

	// Parse the epic to find its "blocks" list (child tickets).
	var raw map[string]interface{}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parsing epic %s: %w", epicID, err)
	}

	ids := make(map[string]bool)
	if blocks, ok := raw["blocks"].([]interface{}); ok {
		for _, b := range blocks {
			if bm, ok := b.(map[string]interface{}); ok {
				if id, ok := bm["id"].(string); ok {
					ids[id] = true
				}
			}
		}
	}

	// Also include the epic's own ID.
	ids[epicID] = true
	return ids, nil
}
