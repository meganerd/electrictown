package provider

import (
	"fmt"
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

func TestValidation_CircularFallback(t *testing.T) {
	bad := []byte(`
providers:
  anthropic:
    type: anthropic
    base_url: https://api.anthropic.com
    api_key: test
models:
  sonnet:
    provider: anthropic
    model: claude-sonnet-4-20250514
  sonnet-copy:
    provider: anthropic
    model: claude-sonnet-4-20250514
roles:
  mayor:
    model: sonnet
    fallbacks: [sonnet-copy]
defaults:
  model: sonnet
`)
	_, err := ParseConfig(bad)
	if err == nil {
		t.Error("expected validation error for fallback resolving to same provider+model as primary")
	}
}

func TestValidation_EmptyProviderType(t *testing.T) {
	bad := []byte(`
providers:
  mystery:
    base_url: https://example.com
models:
  test-model:
    provider: mystery
    model: some-model
roles: {}
defaults:
  model: test-model
`)
	_, err := ParseConfig(bad)
	if err == nil {
		t.Error("expected validation error for empty provider type")
	}
}

func TestValidation_DefaultsFallbackUnknown(t *testing.T) {
	bad := []byte(`
providers:
  anthropic:
    type: anthropic
    base_url: https://api.anthropic.com
models:
  sonnet:
    provider: anthropic
    model: claude-sonnet-4-20250514
roles: {}
defaults:
  model: sonnet
  fallbacks: [nonexistent-model]
`)
	_, err := ParseConfig(bad)
	if err == nil {
		t.Error("expected validation error for defaults fallback referencing unknown model alias")
	}
}

func TestValidation_EmptyConfig(t *testing.T) {
	empty := []byte(``)
	_, err := ParseConfig(empty)
	if err == nil {
		t.Error("expected validation error for empty config with no providers")
	}
}

func TestValidation_DuplicateFallback(t *testing.T) {
	cfg := []byte(`
providers:
  anthropic:
    type: anthropic
    base_url: https://api.anthropic.com
  ollama:
    type: ollama
    base_url: http://localhost:11434
models:
  sonnet:
    provider: anthropic
    model: claude-sonnet-4-20250514
  qwen:
    provider: ollama
    model: qwen3-coder:32b
roles:
  mayor:
    model: sonnet
    fallbacks: [qwen, qwen]
defaults:
  model: sonnet
`)
	_, err := ParseConfig(cfg)
	if err != nil {
		t.Errorf("duplicate fallback should be accepted (redundant but valid), got: %v", err)
	}
}

func TestResolveModel(t *testing.T) {
	cfg, err := ParseConfig(testConfigYAML)
	if err != nil {
		t.Fatalf("ParseConfig failed: %v", err)
	}
	pc, model, err := cfg.ResolveModel("claude-sonnet")
	if err != nil {
		t.Fatalf("ResolveModel(claude-sonnet) failed: %v", err)
	}
	if pc.Type != "anthropic" {
		t.Errorf("expected provider type anthropic, got %s", pc.Type)
	}
	if pc.BaseURL != "https://api.anthropic.com" {
		t.Errorf("expected base_url https://api.anthropic.com, got %s", pc.BaseURL)
	}
	if model != "claude-sonnet-4-20250514" {
		t.Errorf("expected model claude-sonnet-4-20250514, got %s", model)
	}

	// Unknown alias should error.
	_, _, err = cfg.ResolveModel("nonexistent")
	if err == nil {
		t.Error("expected error for unknown model alias")
	}
}

func TestValidation_AuthType_Valid(t *testing.T) {
	// bearer, basic, none, and empty (default) should all pass validation.
	cases := []struct {
		name     string
		authType string
		apiKey   string
	}{
		{"bearer explicit", "bearer", "my-key"},
		{"basic with colon", "basic", "user:pass"},
		{"none with key", "none", "some-key"},
		{"empty (default)", "", "some-key"},
		{"empty auth no key", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			yaml := fmt.Sprintf(`
providers:
  test:
    type: ollama
    base_url: http://localhost:11434
    api_key: "%s"
    auth_type: "%s"
models:
  m:
    provider: test
    model: llama3
roles: {}
defaults:
  model: m
`, tc.apiKey, tc.authType)
			_, err := ParseConfig([]byte(yaml))
			if err != nil {
				t.Errorf("expected valid config for auth_type=%q api_key=%q, got: %v", tc.authType, tc.apiKey, err)
			}
		})
	}
}

func TestValidation_AuthType_Invalid(t *testing.T) {
	bad := []byte(`
providers:
  test:
    type: ollama
    base_url: http://localhost:11434
    api_key: my-key
    auth_type: oauth2
models:
  m:
    provider: test
    model: llama3
roles: {}
defaults:
  model: m
`)
	_, err := ParseConfig(bad)
	if err == nil {
		t.Error("expected validation error for invalid auth_type 'oauth2'")
	}
}

func TestValidation_AuthType_BasicNoColon(t *testing.T) {
	bad := []byte(`
providers:
  test:
    type: ollama
    base_url: http://localhost:11434
    api_key: nocolonhere
    auth_type: basic
models:
  m:
    provider: test
    model: llama3
roles: {}
defaults:
  model: m
`)
	_, err := ParseConfig(bad)
	if err == nil {
		t.Error("expected validation error for basic auth with api_key missing ':'")
	}
}

func TestValidation_AuthType_BearerNoKey(t *testing.T) {
	bad := []byte(`
providers:
  test:
    type: ollama
    base_url: http://localhost:11434
    auth_type: bearer
models:
  m:
    provider: test
    model: llama3
roles: {}
defaults:
  model: m
`)
	_, err := ParseConfig(bad)
	if err == nil {
		t.Error("expected validation error for bearer auth with no api_key")
	}
}

func TestValidation_AuthType_BasicNoKey(t *testing.T) {
	bad := []byte(`
providers:
  test:
    type: ollama
    base_url: http://localhost:11434
    auth_type: basic
models:
  m:
    provider: test
    model: llama3
roles: {}
defaults:
  model: m
`)
	_, err := ParseConfig(bad)
	if err == nil {
		t.Error("expected validation error for basic auth with no api_key")
	}
}

func TestValidation_AuthType_BasicEnvVar(t *testing.T) {
	// When api_key starts with $, the colon check should be skipped
	// because the actual value comes from the environment at runtime.
	cfg := []byte(`
providers:
  test:
    type: ollama
    base_url: http://localhost:11434
    api_key: $OLLAMA_BASIC_CREDS
    auth_type: basic
models:
  m:
    provider: test
    model: llama3
roles: {}
defaults:
  model: m
`)
	_, err := ParseConfig(cfg)
	if err != nil {
		t.Errorf("expected basic auth with $ENV_VAR api_key to skip colon check, got: %v", err)
	}
}

func TestResolveRole_Default(t *testing.T) {
	cfg, err := ParseConfig(testConfigYAML)
	if err != nil {
		t.Fatalf("ParseConfig failed: %v", err)
	}
	// "crew" is not configured in roles, should fall back to defaults (qwen-local).
	pc, model, err := cfg.ResolveRole("crew")
	if err != nil {
		t.Fatalf("ResolveRole(crew) failed: %v", err)
	}
	if pc.Type != "ollama" {
		t.Errorf("expected provider type ollama from default, got %s", pc.Type)
	}
	if model != "qwen3-coder:32b" {
		t.Errorf("expected model qwen3-coder:32b from default, got %s", model)
	}

	// Verify fallbacks come from defaults too.
	fbs := cfg.FallbacksForRole("crew")
	if fbs != nil {
		t.Errorf("expected nil fallbacks from defaults (none configured), got %v", fbs)
	}
}
