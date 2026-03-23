package agent

import (
	"testing"
)

func TestDefaultNormalizerPassthrough(t *testing.T) {
	n := &DefaultNormalizer{}
	result := &Result{
		Stdout:   "some output",
		ExitCode: 0,
	}
	if err := n.Normalize(result); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Stdout != "some output" {
		t.Error("DefaultNormalizer should not modify stdout")
	}
	if len(result.FileChanges) != 0 {
		t.Error("DefaultNormalizer should not add file changes")
	}
}

func TestDiffNormalizerParsesDiff(t *testing.T) {
	diffOutput := `Here's my change:

--- a/main.go
+++ b/main.go
@@ -1,3 +1,4 @@
 package main

+import "fmt"
 func main() {}

Done!`

	n := &DiffNormalizer{}
	result := &Result{Stdout: diffOutput}
	if err := n.Normalize(result); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.FileChanges) != 1 {
		t.Fatalf("expected 1 file change, got %d", len(result.FileChanges))
	}
	fc := result.FileChanges[0]
	if fc.Path != "main.go" {
		t.Errorf("expected path main.go, got %q", fc.Path)
	}
	if fc.Action != ActionModify {
		t.Errorf("expected action modify, got %q", fc.Action)
	}
	if fc.Diff == "" {
		t.Error("expected non-empty diff")
	}
}

func TestDiffNormalizerMultipleFiles(t *testing.T) {
	diffOutput := `--- a/foo.go
+++ b/foo.go
@@ -1 +1 @@
-old
+new
--- a/bar.go
+++ b/bar.go
@@ -1 +1 @@
-old
+new`

	n := &DiffNormalizer{}
	result := &Result{Stdout: diffOutput}
	if err := n.Normalize(result); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.FileChanges) != 2 {
		t.Fatalf("expected 2 file changes, got %d", len(result.FileChanges))
	}
	if result.FileChanges[0].Path != "foo.go" {
		t.Errorf("expected first file foo.go, got %q", result.FileChanges[0].Path)
	}
	if result.FileChanges[1].Path != "bar.go" {
		t.Errorf("expected second file bar.go, got %q", result.FileChanges[1].Path)
	}
}

func TestDiffNormalizerNewFile(t *testing.T) {
	diffOutput := `--- a/new.go
+++ b/new.go
new file mode 100644
@@ -0,0 +1 @@
+package new`

	n := &DiffNormalizer{}
	result := &Result{Stdout: diffOutput}
	if err := n.Normalize(result); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.FileChanges) != 1 {
		t.Fatalf("expected 1 file change, got %d", len(result.FileChanges))
	}
	if result.FileChanges[0].Action != ActionCreate {
		t.Errorf("expected action create, got %q", result.FileChanges[0].Action)
	}
}

func TestDiffNormalizerNoDiff(t *testing.T) {
	n := &DiffNormalizer{}
	result := &Result{Stdout: "just some text with no diffs"}
	if err := n.Normalize(result); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.FileChanges) != 0 {
		t.Errorf("expected 0 file changes, got %d", len(result.FileChanges))
	}
}

func TestGetNormalizer(t *testing.T) {
	n := GetNormalizer(TypeAider)
	if _, ok := n.(*DiffNormalizer); !ok {
		t.Error("expected DiffNormalizer for aider")
	}

	n = GetNormalizer(TypeClaudeCode)
	if _, ok := n.(*DefaultNormalizer); !ok {
		t.Error("expected DefaultNormalizer for claude-code")
	}

	n = GetNormalizer("unknown-type")
	if _, ok := n.(*DefaultNormalizer); !ok {
		t.Error("expected DefaultNormalizer for unknown type")
	}
}

func TestRegisterNormalizer(t *testing.T) {
	custom := &DiffNormalizer{} // Use DiffNormalizer as a custom one.
	RegisterNormalizer(TypeClaudeCode, custom)
	defer RegisterNormalizer(TypeClaudeCode, &DefaultNormalizer{}) // restore

	n := GetNormalizer(TypeClaudeCode)
	if n != custom {
		t.Error("expected custom normalizer after registration")
	}
}
