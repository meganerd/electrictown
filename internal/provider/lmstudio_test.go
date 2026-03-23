package provider

import (
	"testing"
)

// Test that lmstudio provider type is accepted in config.
func TestLMStudioConfig_Valid(t *testing.T) {
	cfg := []byte(`
providers:
  lmstudio-local:
    type: lmstudio
    base_url: http://localhost:1234

models:
  deepseek-local:
    provider: lmstudio-local
    model: deepseek-coder-v2

roles:
  polecat:
    model: deepseek-local

defaults:
  model: deepseek-local
`)
	parsed, err := ParseConfig(cfg)
	if err != nil {
		t.Fatalf("expected valid lmstudio config, got: %v", err)
	}
	if len(parsed.Providers) != 1 {
		t.Errorf("expected 1 provider, got %d", len(parsed.Providers))
	}
	p := parsed.Providers["lmstudio-local"]
	if p.Type != "lmstudio" {
		t.Errorf("expected type lmstudio, got %s", p.Type)
	}
	if p.BaseURL != "http://localhost:1234" {
		t.Errorf("expected base_url http://localhost:1234, got %s", p.BaseURL)
	}
}

// Test lmstudio with no auth (common case — local instance).
func TestLMStudioConfig_NoAuth(t *testing.T) {
	cfg := []byte(`
providers:
  lmstudio:
    type: lmstudio
    base_url: http://192.168.1.100:1234

models:
  m:
    provider: lmstudio
    model: qwen3.5:27b

defaults:
  model: m
roles: {}
`)
	_, err := ParseConfig(cfg)
	if err != nil {
		t.Fatalf("lmstudio with no auth should be valid, got: %v", err)
	}
}

// Test multiple LM Studio instances on different interfaces.
func TestLMStudioConfig_MultipleInstances(t *testing.T) {
	cfg := []byte(`
providers:
  lmstudio-local:
    type: lmstudio
    base_url: http://localhost:1234
  lmstudio-workstation:
    type: lmstudio
    base_url: http://192.168.1.50:1234
  lmstudio-gpu-server:
    type: lmstudio
    base_url: http://gpu-box.local:1234

models:
  qwen-local:
    provider: lmstudio-local
    model: qwen3.5:27b
  qwen-workstation:
    provider: lmstudio-workstation
    model: qwen3.5:35b
  llama-gpu:
    provider: lmstudio-gpu-server
    model: llama-3.3:70b

roles:
  polecat:
    model: qwen-local
    pool: [qwen-local, qwen-workstation, llama-gpu]

defaults:
  model: qwen-local
`)
	parsed, err := ParseConfig(cfg)
	if err != nil {
		t.Fatalf("multi-instance lmstudio config failed: %v", err)
	}
	if len(parsed.Providers) != 3 {
		t.Errorf("expected 3 providers, got %d", len(parsed.Providers))
	}
}

// Test lmstudio with auth (for remote/secured instances).
func TestLMStudioConfig_WithAuth(t *testing.T) {
	cfg := []byte(`
providers:
  lmstudio-remote:
    type: lmstudio
    base_url: http://remote.host:1234
    api_key: test-key
    auth_type: bearer

models:
  m:
    provider: lmstudio-remote
    model: some-model

defaults:
  model: m
roles: {}
`)
	_, err := ParseConfig(cfg)
	if err != nil {
		t.Fatalf("lmstudio with bearer auth should be valid, got: %v", err)
	}
}

// Test that model resolution works through lmstudio provider.
func TestLMStudioConfig_ResolveModel(t *testing.T) {
	cfg := []byte(`
providers:
  lmstudio-local:
    type: lmstudio
    base_url: http://localhost:1234

models:
  qwen-lms:
    provider: lmstudio-local
    model: qwen3.5:27b-q4_K_M

roles:
  polecat:
    model: qwen-lms

defaults:
  model: qwen-lms
`)
	parsed, err := ParseConfig(cfg)
	if err != nil {
		t.Fatalf("ParseConfig failed: %v", err)
	}
	pc, model, err := parsed.ResolveModel("qwen-lms")
	if err != nil {
		t.Fatalf("ResolveModel failed: %v", err)
	}
	if pc.Type != "lmstudio" {
		t.Errorf("expected provider type lmstudio, got %s", pc.Type)
	}
	if model != "qwen3.5:27b-q4_K_M" {
		t.Errorf("expected model qwen3.5:27b-q4_K_M, got %s", model)
	}
}
