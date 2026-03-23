package loop

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestBuildPrompt_Default(t *testing.T) {
	ticket := Ticket{
		ID:          "ET-100",
		Title:       "Add foo feature",
		Description: "Implement the foo feature in bar.go",
		Acceptance:  "foo function exists and passes tests",
	}
	prompt, err := BuildPrompt(ticket, "")
	if err != nil {
		t.Fatalf("BuildPrompt failed: %v", err)
	}
	if !strings.Contains(prompt, "ET-100") {
		t.Error("prompt should contain ticket ID")
	}
	if !strings.Contains(prompt, "Add foo feature") {
		t.Error("prompt should contain ticket title")
	}
	if !strings.Contains(prompt, "Implement the foo feature") {
		t.Error("prompt should contain description")
	}
	if !strings.Contains(prompt, "foo function exists") {
		t.Error("prompt should contain acceptance criteria")
	}
	if !strings.Contains(prompt, "ONLY this one ticket") {
		t.Error("prompt should instruct to implement only one ticket")
	}
}

func TestBuildPrompt_CustomTemplate(t *testing.T) {
	// Write a temp template.
	tmpFile := t.TempDir() + "/template.txt"
	if err := writeFile(tmpFile, "Do {{.ID}}: {{.Title}}"); err != nil {
		t.Fatalf("writing temp template: %v", err)
	}

	ticket := Ticket{ID: "ET-50", Title: "test task"}
	prompt, err := BuildPrompt(ticket, tmpFile)
	if err != nil {
		t.Fatalf("BuildPrompt with custom template failed: %v", err)
	}
	if prompt != "Do ET-50: test task" {
		t.Errorf("unexpected prompt: %q", prompt)
	}
}

func TestBuildPrompt_InvalidTemplate(t *testing.T) {
	ticket := Ticket{ID: "ET-1"}
	_, err := BuildPrompt(ticket, "/nonexistent/template.txt")
	if err == nil {
		t.Error("expected error for nonexistent template")
	}
}

func TestNewRunner_Defaults(t *testing.T) {
	r := NewRunner(Config{})
	if r.cfg.MaxIterations != 20 {
		t.Errorf("expected default MaxIterations 20, got %d", r.cfg.MaxIterations)
	}
	if r.cfg.MaxRetries != 3 {
		t.Errorf("expected default MaxRetries 3, got %d", r.cfg.MaxRetries)
	}
	if r.cfg.Cooldown != 5*time.Second {
		t.Errorf("expected default Cooldown 5s, got %s", r.cfg.Cooldown)
	}
}

func TestPickNext_SkipsEpics(t *testing.T) {
	r := NewRunner(Config{})
	tickets := []Ticket{
		{ID: "ET-1", Type: "epic", Title: "Epic"},
		{ID: "ET-2", Type: "task", Title: "Task"},
	}
	picked := r.pickNext(tickets, nil)
	if picked == nil {
		t.Fatal("expected a ticket to be picked")
	}
	if picked.ID != "ET-2" {
		t.Errorf("expected ET-2, got %s", picked.ID)
	}
}

func TestPickNext_SkipsFailed(t *testing.T) {
	r := NewRunner(Config{})
	tickets := []Ticket{
		{ID: "ET-1", Type: "task", Title: "Failed"},
		{ID: "ET-2", Type: "task", Title: "Good"},
	}
	picked := r.pickNext(tickets, []string{"ET-1"})
	if picked == nil {
		t.Fatal("expected a ticket to be picked")
	}
	if picked.ID != "ET-2" {
		t.Errorf("expected ET-2, got %s", picked.ID)
	}
}

func TestPickNext_AllExhausted(t *testing.T) {
	r := NewRunner(Config{})
	tickets := []Ticket{
		{ID: "ET-1", Type: "task"},
	}
	picked := r.pickNext(tickets, []string{"ET-1"})
	if picked != nil {
		t.Error("expected nil when all tickets are failed")
	}
}

func TestPickNext_EmptyList(t *testing.T) {
	r := NewRunner(Config{})
	picked := r.pickNext(nil, nil)
	if picked != nil {
		t.Error("expected nil for empty ticket list")
	}
}

func TestFormatReport(t *testing.T) {
	result := &Result{
		Completed:  []string{"ET-1", "ET-2"},
		Failed:     []string{"ET-3"},
		Iterations: 5,
		Duration:   2 * time.Minute,
	}
	report := FormatReport(result)
	if !strings.Contains(report, "Completed:   2") {
		t.Error("report should show 2 completed")
	}
	if !strings.Contains(report, "Failed:      1") {
		t.Error("report should show 1 failed")
	}
	if !strings.Contains(report, "ET-1") {
		t.Error("report should list completed ticket IDs")
	}
	if !strings.Contains(report, "ET-3") {
		t.Error("report should list failed ticket IDs")
	}
}

func TestEpicSuffix(t *testing.T) {
	if s := epicSuffix(""); s != "" {
		t.Errorf("expected empty string, got %q", s)
	}
	if s := epicSuffix("ET-251"); s != " (scoped to ET-251)" {
		t.Errorf("unexpected suffix: %q", s)
	}
}

// writeFile is a test helper.
func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0644)
}
