package provider

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDumpFailedRequest_CreatesFile(t *testing.T) {
	// Override home to temp dir.
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	messages := []Message{
		{Role: RoleSystem, Content: "You are a worker."},
		{Role: RoleUser, Content: "Write some code."},
	}

	path, err := DumpFailedRequest("test-model", messages, errors.New("connection refused"))
	if err != nil {
		t.Fatalf("DumpFailedRequest: %v", err)
	}

	if !strings.Contains(path, "failed_requests") {
		t.Errorf("path should contain 'failed_requests': %s", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var dump FailedRequestDump
	if err := json.Unmarshal(data, &dump); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if dump.Model != "test-model" {
		t.Errorf("model = %q, want %q", dump.Model, "test-model")
	}
	if dump.NumMessages != 2 {
		t.Errorf("num_messages = %d, want 2", dump.NumMessages)
	}
	if dump.ErrorMsg != "connection refused" {
		t.Errorf("error_message = %q, want %q", dump.ErrorMsg, "connection refused")
	}
}

func TestDumpFailedRequest_TruncatesLongSystemPrompt(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	longPrompt := strings.Repeat("x", 1000)
	messages := []Message{
		{Role: RoleSystem, Content: longPrompt},
		{Role: RoleUser, Content: "task"},
	}

	path, err := DumpFailedRequest("model", messages, errors.New("err"))
	if err != nil {
		t.Fatalf("DumpFailedRequest: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var dump FailedRequestDump
	if err := json.Unmarshal(data, &dump); err != nil {
		t.Fatal(err)
	}

	if len(dump.Messages[0].Content) >= 1000 {
		t.Errorf("system prompt not truncated: len=%d", len(dump.Messages[0].Content))
	}
	if !strings.HasSuffix(dump.Messages[0].Content, "[truncated]") {
		t.Error("truncated system prompt should end with [truncated]")
	}
}

func TestDumpFailedRequest_TokenEstimate(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	messages := []Message{
		{Role: RoleUser, Content: strings.Repeat("a", 400)},
	}

	path, err := DumpFailedRequest("model", messages, errors.New("err"))
	if err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	var dump FailedRequestDump
	json.Unmarshal(data, &dump)

	if dump.TokenEst != 100 {
		t.Errorf("token estimate = %d, want 100 (400 chars / 4)", dump.TokenEst)
	}
}

func TestDumpFailedRequest_ErrorClassification(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	apiErr := &APIError{Status: 429, Message: "rate limited"}
	path, err := DumpFailedRequest("model", nil, apiErr)
	if err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	var dump FailedRequestDump
	json.Unmarshal(data, &dump)

	if dump.ErrorType != "rate_limit" {
		t.Errorf("error_type = %q, want %q", dump.ErrorType, "rate_limit")
	}
}

func TestDumpFailedRequest_DirCreation(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	// Dir shouldn't exist yet.
	dumpDir := filepath.Join(dir, ".electrictown", "failed_requests")
	if _, err := os.Stat(dumpDir); !os.IsNotExist(err) {
		t.Fatal("dump dir should not exist yet")
	}

	_, err := DumpFailedRequest("model", nil, errors.New("err"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(dumpDir); err != nil {
		t.Errorf("dump dir should exist after DumpFailedRequest: %v", err)
	}
}
