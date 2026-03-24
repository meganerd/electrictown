package agent

import (
	"testing"
)

func TestAgentTypeConstants(t *testing.T) {
	types := ValidAgentTypes()
	if len(types) != 8 {
		t.Errorf("expected 8 agent types, got %d", len(types))
	}
	// Verify all expected types are present.
	expected := []AgentType{
		TypeClaudeCode, TypeCodex, TypeAider,
		TypeOpenCode, TypeOpenClaw, TypeGeminiCLI,
		TypeCursor, TypeSkill,
	}
	for _, e := range expected {
		if !IsValidAgentType(e) {
			t.Errorf("expected %q to be a valid agent type", e)
		}
	}
}

func TestIsValidAgentType(t *testing.T) {
	tests := []struct {
		agentType AgentType
		valid     bool
	}{
		{TypeClaudeCode, true},
		{TypeCodex, true},
		{TypeAider, true},
		{TypeOpenCode, true},
		{TypeOpenClaw, true},
		{TypeGeminiCLI, true},
		{"invalid", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := IsValidAgentType(tt.agentType); got != tt.valid {
			t.Errorf("IsValidAgentType(%q) = %v, want %v", tt.agentType, got, tt.valid)
		}
	}
}

func TestResultSuccess(t *testing.T) {
	tests := []struct {
		exitCode int
		success  bool
	}{
		{0, true},
		{1, false},
		{-1, false},
		{127, false},
	}
	for _, tt := range tests {
		r := &Result{ExitCode: tt.exitCode}
		if got := r.Success(); got != tt.success {
			t.Errorf("Result{ExitCode: %d}.Success() = %v, want %v", tt.exitCode, got, tt.success)
		}
	}
}

func TestTaskFields(t *testing.T) {
	task := Task{
		Prompt:     "write a function",
		WorkingDir: "/tmp/project",
		Env:        map[string]string{"FOO": "bar"},
		Model:      "claude-sonnet-4-20250514",
		Flags:      []string{"--max-turns", "5"},
	}
	if task.Prompt != "write a function" {
		t.Error("Prompt field not set correctly")
	}
	if task.WorkingDir != "/tmp/project" {
		t.Error("WorkingDir field not set correctly")
	}
	if task.Env["FOO"] != "bar" {
		t.Error("Env field not set correctly")
	}
	if task.Model != "claude-sonnet-4-20250514" {
		t.Error("Model field not set correctly")
	}
	if len(task.Flags) != 2 {
		t.Error("Flags field not set correctly")
	}
}

func TestFileChangeFields(t *testing.T) {
	fc := FileChange{
		Path:    "main.go",
		Action:  ActionCreate,
		Content: "package main",
	}
	if fc.Path != "main.go" {
		t.Error("Path not set")
	}
	if fc.Action != ActionCreate {
		t.Error("Action not set")
	}
	if fc.Content != "package main" {
		t.Error("Content not set")
	}
}
