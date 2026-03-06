package pool

import "testing"

func TestParseSpecialistAssignment(t *testing.T) {
	tests := []struct {
		name     string
		subtask  string
		expected string
	}{
		{"basic marker", "Build React form [specialist: frontend-dev]", "frontend-dev"},
		{"with depends", "Create schema [specialist: database-admin] [depends: 1]", "database-admin"},
		{"case insensitive", "Task [Specialist: Backend-Dev]", "Backend-Dev"},
		{"extra spaces", "Task [specialist:  frontend-dev  ]", "frontend-dev"},
		{"no marker", "Build the API endpoint", ""},
		{"empty subtask", "", ""},
		{"marker at start", "[specialist: infra] Deploy to production", "infra"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseSpecialistAssignment(tt.subtask)
			if got != tt.expected {
				t.Errorf("ParseSpecialistAssignment(%q) = %q, want %q", tt.subtask, got, tt.expected)
			}
		})
	}
}

func TestStripSpecialistMarkers(t *testing.T) {
	tests := []struct {
		name     string
		subtask  string
		expected string
	}{
		{"basic strip", "Build form [specialist: frontend-dev]", "Build form"},
		{"with depends preserved", "Create schema [specialist: db] [depends: 1]", "Create schema [depends: 1]"},
		{"no marker", "Build the API", "Build the API"},
		{"multiple markers", "Task [specialist: a] [specialist: b]", "Task"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StripSpecialistMarkers(tt.subtask)
			if got != tt.expected {
				t.Errorf("StripSpecialistMarkers(%q) = %q, want %q", tt.subtask, got, tt.expected)
			}
		})
	}
}

func TestFuzzyMatchSpecialist(t *testing.T) {
	known := []string{"frontend-dev", "backend-dev", "database-admin"}

	tests := []struct {
		name      string
		input     string
		wantMatch string
		wantOK    bool
	}{
		{"exact match", "frontend-dev", "frontend-dev", true},
		{"case insensitive exact", "Frontend-Dev", "frontend-dev", true},
		{"one char off", "frontnd-dev", "frontend-dev", true},
		{"two chars off", "frntend-dev", "frontend-dev", true},
		{"too far", "xyz-worker", "", false},
		{"empty input", "", "", false},
		{"close to backend", "backend-de", "backend-dev", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			match, ok := FuzzyMatchSpecialist(tt.input, known)
			if ok != tt.wantOK {
				t.Errorf("FuzzyMatchSpecialist(%q) ok = %v, want %v", tt.input, ok, tt.wantOK)
			}
			if match != tt.wantMatch {
				t.Errorf("FuzzyMatchSpecialist(%q) match = %q, want %q", tt.input, match, tt.wantMatch)
			}
		})
	}
}

func TestLevenshtein(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "", 3},
		{"", "abc", 3},
		{"abc", "abc", 0},
		{"abc", "abd", 1},
		{"abc", "adc", 1},
		{"kitten", "sitting", 3},
	}

	for _, tt := range tests {
		t.Run(tt.a+"_"+tt.b, func(t *testing.T) {
			got := levenshtein(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("levenshtein(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}
