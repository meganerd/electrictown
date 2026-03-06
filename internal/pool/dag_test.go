package pool

import (
	"sort"
	"testing"
)

func TestParseDependencies_NoDeps(t *testing.T) {
	subtasks := []string{"Create model", "Build API", "Write tests"}
	deps := ParseDependencies(subtasks)
	if len(deps) != 0 {
		t.Errorf("expected empty deps, got %v", deps)
	}
}

func TestParseDependencies_SingleDep(t *testing.T) {
	subtasks := []string{"Create User model", "Build User API [depends: 1]"}
	deps := ParseDependencies(subtasks)
	if len(deps) != 1 {
		t.Fatalf("expected 1 dep entry, got %d: %v", len(deps), deps)
	}
	got := deps[1]
	if len(got) != 1 || got[0] != 0 {
		t.Errorf("deps[1] = %v, want [0]", got)
	}
}

func TestParseDependencies_MultipleDeps(t *testing.T) {
	subtasks := []string{"Create model", "Build API", "Integration tests [depends: 1, 2]"}
	deps := ParseDependencies(subtasks)
	got := deps[2]
	sort.Ints(got)
	if len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Errorf("deps[2] = %v, want [0, 1]", got)
	}
}

func TestParseDependencies_CaseInsensitive(t *testing.T) {
	subtasks := []string{"First task", "Second task [Depends: 1]"}
	deps := ParseDependencies(subtasks)
	if len(deps) != 1 {
		t.Errorf("expected 1 dep entry (case-insensitive), got %d", len(deps))
	}
}

func TestStripDepMarkers_HasMarker(t *testing.T) {
	input := "Build User API [depends: 1]"
	got := StripDepMarkers(input)
	want := "Build User API"
	if got != want {
		t.Errorf("StripDepMarkers(%q) = %q, want %q", input, got, want)
	}
}

func TestStripDepMarkers_NoMarker(t *testing.T) {
	input := "Create User model"
	got := StripDepMarkers(input)
	if got != input {
		t.Errorf("StripDepMarkers(%q) = %q, want unchanged", input, got)
	}
}

func TestTopoSort_NoDeps(t *testing.T) {
	waves, err := TopoSort(3, map[int][]int{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(waves) != 1 {
		t.Fatalf("expected 1 wave, got %d: %v", len(waves), waves)
	}
	sort.Ints(waves[0])
	if len(waves[0]) != 3 || waves[0][0] != 0 || waves[0][1] != 1 || waves[0][2] != 2 {
		t.Errorf("wave[0] = %v, want [0,1,2]", waves[0])
	}
}

func TestTopoSort_LinearChain(t *testing.T) {
	// 0 → 1 → 2 (2 depends on 1, 1 depends on 0)
	deps := map[int][]int{
		1: {0},
		2: {1},
	}
	waves, err := TopoSort(3, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(waves) != 3 {
		t.Fatalf("expected 3 waves, got %d: %v", len(waves), waves)
	}
	if waves[0][0] != 0 {
		t.Errorf("wave[0] = %v, want [0]", waves[0])
	}
	if waves[1][0] != 1 {
		t.Errorf("wave[1] = %v, want [1]", waves[1])
	}
	if waves[2][0] != 2 {
		t.Errorf("wave[2] = %v, want [2]", waves[2])
	}
}

func TestTopoSort_Diamond(t *testing.T) {
	// 0 and 1 independent; 2 depends on both
	deps := map[int][]int{
		2: {0, 1},
	}
	waves, err := TopoSort(3, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(waves) != 2 {
		t.Fatalf("expected 2 waves, got %d: %v", len(waves), waves)
	}
	sort.Ints(waves[0])
	if len(waves[0]) != 2 || waves[0][0] != 0 || waves[0][1] != 1 {
		t.Errorf("wave[0] = %v, want [0,1]", waves[0])
	}
	if len(waves[1]) != 1 || waves[1][0] != 2 {
		t.Errorf("wave[1] = %v, want [2]", waves[1])
	}
}

func TestTopoSort_CycleDetected(t *testing.T) {
	deps := map[int][]int{
		0: {1},
		1: {0},
	}
	_, err := TopoSort(2, deps)
	if err == nil {
		t.Fatal("expected error for cycle, got nil")
	}
}

func TestTopoSort_Empty(t *testing.T) {
	waves, err := TopoSort(0, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(waves) != 0 {
		t.Errorf("expected empty waves, got %v", waves)
	}
}

func TestHasDependencies_Empty(t *testing.T) {
	if HasDependencies(map[int][]int{}) {
		t.Error("expected false for empty map")
	}
}

func TestHasDependencies_WithDeps(t *testing.T) {
	if !HasDependencies(map[int][]int{1: {0}}) {
		t.Error("expected true for map with dependencies")
	}
}
