package build

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// BuildError represents a single compiler error with file attribution.
type BuildError struct {
	File    string // relative path from the output directory
	Line    int
	Message string
}

// goErrorPattern matches Go compiler output: "path/file.go:line:col: message"
// Also handles "path/file.go:line: message" (no column).
var goErrorPattern = regexp.MustCompile(`^([^:\n]+\.go):(\d+)(?::\d+)?:\s+(.+)$`)

// ParseBuildErrors extracts file-attributed errors from compiler stderr output.
// Currently handles Go compiler format; other formats produce file-less entries.
func ParseBuildErrors(stderr string) []BuildError {
	var errs []BuildError
	seen := map[string]bool{}

	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		m := goErrorPattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		file := filepath.Clean(m[1])
		lineNum, _ := strconv.Atoi(m[2])
		msg := m[3]

		key := file + ":" + strconv.Itoa(lineNum) + ":" + msg
		if seen[key] {
			continue
		}
		seen[key] = true
		errs = append(errs, BuildError{File: file, Line: lineNum, Message: msg})
	}
	return errs
}

// NormalizeErrorPaths strips an absolute outputDir prefix from error file paths,
// converting them to paths relative to outputDir.
func NormalizeErrorPaths(errs []BuildError, outputDir string) []BuildError {
	out := make([]BuildError, len(errs))
	for i, e := range errs {
		rel := e.File
		if filepath.IsAbs(rel) {
			if r, err := filepath.Rel(outputDir, rel); err == nil {
				rel = r
			}
		}
		out[i] = BuildError{File: rel, Line: e.Line, Message: e.Message}
	}
	return out
}

// MapFilesToWorkers returns a map from worker index to the build errors
// attributed to files that worker produced. fileWorkerMap maps relative
// file paths to worker indices (0-based).
func MapFilesToWorkers(errs []BuildError, fileWorkerMap map[string]int) map[int][]BuildError {
	result := make(map[int][]BuildError)
	for _, e := range errs {
		idx, ok := fileWorkerMap[e.File]
		if !ok {
			// Try cleaning the path once more.
			idx, ok = fileWorkerMap[filepath.Clean(e.File)]
		}
		if ok {
			result[idx] = append(result[idx], e)
		}
	}
	return result
}

// ErrorSummary returns the first n lines of stderr as a brief display string.
func ErrorSummary(stderr string, n int) string {
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	if len(lines) > n {
		lines = lines[:n]
		lines = append(lines, fmt.Sprintf("... (%d lines truncated)", len(strings.Split(stderr, "\n"))-n))
	}
	return strings.Join(lines, "\n")
}
