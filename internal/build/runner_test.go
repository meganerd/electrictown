package build

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectRunner(t *testing.T) {
	tests := []struct {
		name     string
		files    []string
		wantName string
		wantNil  bool
	}{
		{"go.mod detected", []string{"go.mod"}, "go", false},
		{"package.json detected", []string{"package.json"}, "node", false},
		{"Makefile detected", []string{"Makefile"}, "make", false},
		{"go.mod wins over Makefile", []string{"go.mod", "Makefile"}, "go", false},
		{"empty dir returns nil", []string{}, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, f := range tt.files {
				if err := os.WriteFile(filepath.Join(dir, f), []byte(""), 0644); err != nil {
					t.Fatal(err)
				}
			}
			r := DetectRunner(dir)
			if tt.wantNil {
				if r != nil {
					t.Errorf("want nil runner, got %q", r.Name())
				}
				return
			}
			if r == nil {
				t.Fatalf("want runner %q, got nil", tt.wantName)
			}
			if r.Name() != tt.wantName {
				t.Errorf("want runner %q, got %q", tt.wantName, r.Name())
			}
		})
	}
}

func TestParseBuildErrors(t *testing.T) {
	stderr := `
# example.com/myapp
cmd/main.go:12:5: undefined: DoThing
cmd/main.go:15: cannot use x (type int) as type string
internal/pkg/foo.go:42:1: syntax error: unexpected }
not a go error line
`
	errs := ParseBuildErrors(stderr)
	if len(errs) != 3 {
		t.Fatalf("want 3 errors, got %d: %+v", len(errs), errs)
	}

	tests := []struct {
		file    string
		line    int
		message string
	}{
		{"cmd/main.go", 12, "undefined: DoThing"},
		{"cmd/main.go", 15, "cannot use x (type int) as type string"},
		{"internal/pkg/foo.go", 42, "syntax error: unexpected }"},
	}

	for i, tt := range tests {
		if errs[i].File != tt.file {
			t.Errorf("[%d] file: want %q, got %q", i, tt.file, errs[i].File)
		}
		if errs[i].Line != tt.line {
			t.Errorf("[%d] line: want %d, got %d", i, tt.line, errs[i].Line)
		}
		if errs[i].Message != tt.message {
			t.Errorf("[%d] message: want %q, got %q", i, tt.message, errs[i].Message)
		}
	}
}

func TestMapFilesToWorkers(t *testing.T) {
	errs := []BuildError{
		{File: "cmd/main.go", Line: 12, Message: "undefined: DoThing"},
		{File: "internal/pkg/foo.go", Line: 42, Message: "syntax error"},
		{File: "cmd/main.go", Line: 15, Message: "type mismatch"},
		{File: "unknown/file.go", Line: 1, Message: "no worker"},
	}

	fileWorkerMap := map[string]int{
		"cmd/main.go":         0,
		"internal/pkg/foo.go": 1,
	}

	result := MapFilesToWorkers(errs, fileWorkerMap)

	if len(result[0]) != 2 {
		t.Errorf("worker 0: want 2 errors, got %d", len(result[0]))
	}
	if len(result[1]) != 1 {
		t.Errorf("worker 1: want 1 error, got %d", len(result[1]))
	}
	if _, ok := result[2]; ok {
		t.Errorf("worker 2 should not appear (file unattributed)")
	}
}

func TestParseBuildErrors_Dedup(t *testing.T) {
	stderr := "cmd/main.go:5:3: undefined: Foo\ncmd/main.go:5:3: undefined: Foo\n"
	errs := ParseBuildErrors(stderr)
	if len(errs) != 1 {
		t.Errorf("want 1 deduplicated error, got %d", len(errs))
	}
}
