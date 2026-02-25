package provider

import (
	"testing"
)

var testConfigYAML = []byte(`
providers:
  anthropic:
    type: anthropic
    base_url: https://api.anthropic.com
    api_key: test-key
  ollama-local:
    type: ollama
    base_url: http://localhost:11434

models:
  claude-sonnet:
    provider: anthropic
    model: claude-sonnet-4-20250514
  qwen-local:
    provider: ollama-local
    model: qwen3-coder:32b

roles:
  mayor:
    model: claude-sonnet
    fallbacks: [qwen-local]
  polecat:
    model: qwen-local

defaults:
  model: qwen-local
  max_tokens: 4096
`)

func TestParseConfig(t *testing.T) {
	cfg, err := ParseConfig(testConfigYAML)
	if err != nil {
		t.Fatalf("ParseConfig failed: %v", err)
	}
	if len(cfg.Providers) != 2 {
		t.Errorf("expected 2 providers, got %d", len(cfg.Providers))
	}
	if len(cfg.Models) != 2 {
		t.Errorf("expected 2 models, got %d", len(cfg.Models))
	}
	if len(cfg.Roles) != 2 {
		t.Errorf("expected 2 roles, got %d", len(cfg.Roles))
	}
}

func TestResolveRole(t *testing.T) {
	cfg, err := ParseConfig(testConfigYAML)
	if err != nil {
		t.Fatalf("ParseConfig failed: %v", err)
	}

	// Resolve a configured role.
	pc, model, err := cfg.ResolveRole("mayor")
	if err != nil {
		t.Fatalf("ResolveRole(mayor) failed: %v", err)
	}
	if pc.Type != "anthropic" {
		t.Errorf("expected anthropic, got %s", pc.Type)
	}
	if model != "claude-sonnet-4-20250514" {
		t.Errorf("expected claude-sonnet-4-20250514, got %s", model)
	}

	// Resolve an unconfigured role falls back to default.
	_, model, err = cfg.ResolveRole("unknown-role")
	if err != nil {
		t.Fatalf("ResolveRole(unknown) failed: %v", err)
	}
	if model != "qwen3-coder:32b" {
		t.Errorf("expected qwen3-coder:32b from default, got %s", model)
	}
}

func TestFallbacks(t *testing.T) {
	cfg, err := ParseConfig(testConfigYAML)
	if err != nil {
		t.Fatalf("ParseConfig failed: %v", err)
	}
	fbs := cfg.FallbacksForRole("mayor")
	if len(fbs) != 1 || fbs[0] != "qwen-local" {
		t.Errorf("expected [qwen-local], got %v", fbs)
	}
}

func TestValidation_UnknownProvider(t *testing.T) {
	bad := []byte(`
providers:
  openai:
    type: openai
    base_url: https://api.openai.com/v1
models:
  gpt4:
    provider: nonexistent
    model: gpt-4
roles: {}
defaults: {}
`)
	_, err := ParseConfig(bad)
	if err == nil {
		t.Error("expected validation error for unknown provider")
	}
}

func TestValidation_UnknownModelAlias(t *testing.T) {
	bad := []byte(`
providers:
  openai:
    type: openai
    base_url: https://api.openai.com/v1
models: {}
roles:
  mayor:
    model: nonexistent
defaults: {}
`)
	_, err := ParseConfig(bad)
	if err == nil {
		t.Error("expected validation error for unknown model alias in role")
	}
}
