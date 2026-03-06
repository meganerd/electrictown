package pool

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// depPattern matches [depends: 1], [depends: 1,2], [depends: 1, 3] (case-insensitive).
var depPattern = regexp.MustCompile(`(?i)\[depends:\s*([\d,\s]+)\]`)

// ParseDependencies scans subtask strings for [depends: N] markers and returns
// a dependency map: taskIndex → []dependencyIndices (0-indexed).
// Markers use 1-indexed subtask numbers; this function converts to 0-indexed.
func ParseDependencies(subtasks []string) map[int][]int {
	deps := make(map[int][]int)
	for i, st := range subtasks {
		matches := depPattern.FindStringSubmatch(st)
		if matches == nil {
			continue
		}
		parts := strings.Split(matches[1], ",")
		var indices []int
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			n, err := strconv.Atoi(p)
			if err != nil {
				continue
			}
			// Convert from 1-indexed marker to 0-indexed.
			idx := n - 1
			if idx >= 0 && idx < len(subtasks) {
				indices = append(indices, idx)
			}
		}
		if len(indices) > 0 {
			deps[i] = indices
		}
	}
	return deps
}

// StripDepMarkers removes [depends: ...] markers from a subtask string.
func StripDepMarkers(subtask string) string {
	return strings.TrimSpace(depPattern.ReplaceAllString(subtask, ""))
}

// TopoSort performs a topological sort using Kahn's algorithm, returning
// execution waves. Each wave is a slice of task indices that can run in parallel
// because all their dependencies have been satisfied by prior waves.
//
// Returns an error if a dependency cycle is detected.
func TopoSort(n int, deps map[int][]int) ([][]int, error) {
	if n == 0 {
		return nil, nil
	}

	// Build in-degree counts and adjacency list (forward edges).
	inDegree := make([]int, n)
	// forward[a] = list of tasks that depend on a.
	forward := make([][]int, n)
	for i := 0; i < n; i++ {
		forward[i] = nil
	}
	for task, depList := range deps {
		inDegree[task] = len(depList)
		for _, dep := range depList {
			forward[dep] = append(forward[dep], task)
		}
	}

	// First wave: all tasks with no dependencies.
	var waves [][]int
	var current []int
	for i := 0; i < n; i++ {
		if inDegree[i] == 0 {
			current = append(current, i)
		}
	}
	if len(current) == 0 {
		return nil, fmt.Errorf("cycle detected: no tasks with zero in-degree")
	}

	visited := 0
	for len(current) > 0 {
		waves = append(waves, current)
		visited += len(current)

		var next []int
		for _, task := range current {
			for _, dependent := range forward[task] {
				inDegree[dependent]--
				if inDegree[dependent] == 0 {
					next = append(next, dependent)
				}
			}
		}
		current = next
	}

	if visited != n {
		return nil, fmt.Errorf("cycle detected: visited %d of %d tasks", visited, n)
	}
	return waves, nil
}

// HasDependencies returns true if the dependency map contains at least one
// task with dependencies.
func HasDependencies(deps map[int][]int) bool {
	for _, v := range deps {
		if len(v) > 0 {
			return true
		}
	}
	return false
}
