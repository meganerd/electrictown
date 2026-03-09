package decision

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestNewLogger_EmptyPath(t *testing.T) {
	l, err := NewLogger("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if l != nil {
		t.Error("empty path should return nil logger")
	}
}

func TestNilLogger_LogIsNoop(t *testing.T) {
	var l *Logger
	// Should not panic.
	l.Log(Decision{Phase: "test"})
}

func TestNilLogger_CloseIsNoop(t *testing.T) {
	var l *Logger
	if err := l.Close(); err != nil {
		t.Errorf("nil close: %v", err)
	}
}

func TestLogger_WritesJSONL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "decisions.jsonl")

	l, err := NewLogger(path)
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}

	l.Log(Decision{
		Phase:     "decompose",
		Agent:     "mayor",
		Intent:    "split task into subtasks",
		Action:    "produced 5 subtasks",
		Outcome:   "success",
		Detail:    "5 subtasks generated",
		TokenCost: 150,
	})
	l.Log(Decision{
		Phase:   "review",
		Agent:   "reviewer",
		Intent:  "score worker output",
		Action:  "scored 8/10",
		Outcome: "success",
	})

	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	count := 0
	for scanner.Scan() {
		var d Decision
		if err := json.Unmarshal(scanner.Bytes(), &d); err != nil {
			t.Errorf("line %d: invalid JSON: %v", count+1, err)
		}
		if d.Timestamp == "" {
			t.Errorf("line %d: timestamp should be auto-set", count+1)
		}
		count++
	}
	if count != 2 {
		t.Errorf("expected 2 lines, got %d", count)
	}
}

func TestLogger_ConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "concurrent.jsonl")

	l, err := NewLogger(path)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			l.Log(Decision{Phase: "test", Agent: "worker", Intent: "concurrent write"})
		}(i)
	}
	wg.Wait()

	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	// Count lines.
	data, _ := os.ReadFile(path)
	lines := 0
	for _, b := range data {
		if b == '\n' {
			lines++
		}
	}
	if lines != 50 {
		t.Errorf("expected 50 lines, got %d", lines)
	}
}

func TestLogger_PreservesExplicitTimestamp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ts.jsonl")

	l, err := NewLogger(path)
	if err != nil {
		t.Fatal(err)
	}

	l.Log(Decision{Timestamp: "2026-01-01T00:00:00Z", Phase: "test"})
	l.Close()

	data, _ := os.ReadFile(path)
	var d Decision
	json.Unmarshal(data, &d)
	if d.Timestamp != "2026-01-01T00:00:00Z" {
		t.Errorf("timestamp = %q, want explicit value", d.Timestamp)
	}
}

func TestNewLogger_CreatesParentDirectories(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "a", "b", "c", "decisions.jsonl")

	l, err := NewLogger(nested)
	if err != nil {
		t.Fatalf("NewLogger failed for nested path: %v", err)
	}
	defer l.Close()

	// Verify the directory was created
	fi, err := os.Stat(filepath.Dir(nested))
	if err != nil {
		t.Fatalf("parent directory not created: %v", err)
	}
	if !fi.IsDir() {
		t.Error("expected directory, got file")
	}

	// Verify logging works
	l.Log(Decision{Phase: "test", Agent: "test"})
	l.Close()

	data, err := os.ReadFile(nested)
	if err != nil {
		t.Fatalf("cannot read log file: %v", err)
	}
	if len(data) == 0 {
		t.Error("log file is empty after writing")
	}
}

func TestNewLogger_ErrorMessageOnFailure(t *testing.T) {
	// Try to create logger in a path where we can't write
	// (file as parent directory — can't mkdir through a file)
	dir := t.TempDir()
	blockingFile := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blockingFile, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	badPath := filepath.Join(blockingFile, "sub", "decisions.jsonl")
	_, err := NewLogger(badPath)
	if err == nil {
		t.Fatal("expected error when parent is a file, got nil")
	}
	// Error should be descriptive
	if !contains(err.Error(), "create log directory") && !contains(err.Error(), "not a directory") {
		t.Errorf("error should mention directory creation, got: %v", err)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsInner(s, substr))
}

func containsInner(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
