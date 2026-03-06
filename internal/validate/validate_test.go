package validate

import (
	"strings"
	"testing"
)

func TestValidateFileBlocks(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantOK    bool
		wantError string // substring expected in at least one error; empty = no check
	}{
		{
			name:   "valid single block",
			input:  "===FILE: main.go===\npackage main\n===ENDFILE===",
			wantOK: true,
		},
		{
			name:   "valid multiple blocks",
			input:  "===FILE: a.go===\npackage a\n===ENDFILE===\n===FILE: b.go===\npackage b\n===ENDFILE===",
			wantOK: true,
		},
		{
			name:      "missing ENDFILE",
			input:     "===FILE: main.go===\npackage main\n",
			wantOK:    false,
			wantError: "missing ===ENDFILE===",
		},
		{
			name:      "empty path",
			input:     "===FILE: ===\ncontent\n===ENDFILE===",
			wantOK:    false,
			wantError: "empty file path",
		},
		{
			name:      "path traversal",
			input:     "===FILE: ../../etc/passwd===\nbad\n===ENDFILE===",
			wantOK:    false,
			wantError: "path traversal",
		},
		{
			name:      "no markers",
			input:     "just some plain text response with no file markers",
			wantOK:    false,
			wantError: "no ===FILE: markers",
		},
		{
			name:      "unbalanced markers",
			input:     "===FILE: a.go===\ncode\n===ENDFILE===\n===FILE: b.go===\nmore code\n",
			wantOK:    false,
			wantError: "unbalanced markers",
		},
		{
			name:      "empty input",
			input:     "",
			wantOK:    false,
			wantError: "no ===FILE: markers",
		},
		{
			name:   "path with spaces trimmed",
			input:  "===FILE:  internal/pkg/foo.go ===\npackage pkg\n===ENDFILE===",
			wantOK: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ok, errs := ValidateFileBlocks(tc.input)
			if ok != tc.wantOK {
				t.Errorf("ok = %v, want %v; errors: %v", ok, tc.wantOK, errs)
			}
			if tc.wantError != "" {
				found := false
				for _, e := range errs {
					if strings.Contains(e, tc.wantError) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error containing %q, got: %v", tc.wantError, errs)
				}
			}
		})
	}
}
