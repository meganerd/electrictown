package pool

import (
	"regexp"
	"strings"
)

// specialistPattern matches [specialist: name] markers in subtask text.
var specialistPattern = regexp.MustCompile(`(?i)\[specialist:\s*([^\]]+)\]`)

// ParseSpecialistAssignment extracts the specialist name from a subtask string.
// Returns the trimmed specialist name, or an empty string if no marker is found.
func ParseSpecialistAssignment(subtask string) string {
	matches := specialistPattern.FindStringSubmatch(subtask)
	if matches == nil || len(matches) < 2 {
		return ""
	}
	return strings.TrimSpace(matches[1])
}

// multiSpacePattern collapses runs of multiple spaces into one.
var multiSpacePattern = regexp.MustCompile(`\s{2,}`)

// StripSpecialistMarkers removes all [specialist: ...] markers from a subtask string.
func StripSpecialistMarkers(subtask string) string {
	result := specialistPattern.ReplaceAllString(subtask, "")
	result = multiSpacePattern.ReplaceAllString(result, " ")
	return strings.TrimSpace(result)
}

// FuzzyMatchSpecialist finds the closest match for name in the known specialist list.
// Returns the matched name and true if Levenshtein distance <= 2, or ("", false) if no match.
func FuzzyMatchSpecialist(name string, known []string) (string, bool) {
	name = strings.ToLower(name)
	bestDist := 3 // threshold + 1
	bestMatch := ""
	for _, k := range known {
		d := levenshtein(name, strings.ToLower(k))
		if d < bestDist {
			bestDist = d
			bestMatch = k
		}
	}
	if bestDist <= 2 && bestMatch != "" {
		return bestMatch, true
	}
	return "", false
}

// levenshtein computes the Levenshtein edit distance between two strings.
func levenshtein(a, b string) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}

	// Use single-row DP for space efficiency.
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= len(a); i++ {
		curr := make([]int, len(b)+1)
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min3(
				prev[j]+1,      // deletion
				curr[j-1]+1,    // insertion
				prev[j-1]+cost, // substitution
			)
		}
		prev = curr
	}
	return prev[len(b)]
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}
