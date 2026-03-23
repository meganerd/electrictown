package provider

import (
	"testing"
)

func TestGroqConfig_Valid(t *testing.T) {
	cfg := []byte(`
providers:
  groq:
    type: groq
    base_url: https://api.groq.com/openai/v1
    api_key: test-key
    auth_type: bearer
models:
  llama-groq:
    provider: groq
    model: llama-3.3-70b-versatile
defaults:
  model: llama-groq
roles: {}
`)
	parsed, err := ParseConfig(cfg)
	if err != nil {
		t.Fatalf("expected valid groq config, got: %v", err)
	}
	if parsed.Providers["groq"].Type != "groq" {
		t.Error("expected type groq")
	}
}

func TestMistralConfig_Valid(t *testing.T) {
	cfg := []byte(`
providers:
  mistral:
    type: mistral
    base_url: https://api.mistral.ai/v1
    api_key: test-key
    auth_type: bearer
models:
  codestral:
    provider: mistral
    model: codestral-latest
defaults:
  model: codestral
roles: {}
`)
	parsed, err := ParseConfig(cfg)
	if err != nil {
		t.Fatalf("expected valid mistral config, got: %v", err)
	}
	if parsed.Providers["mistral"].Type != "mistral" {
		t.Error("expected type mistral")
	}
}

func TestCerebrasConfig_Valid(t *testing.T) {
	cfg := []byte(`
providers:
  cerebras:
    type: cerebras
    base_url: https://api.cerebras.ai/v1
    api_key: test-key
    auth_type: bearer
models:
  llama-cerebras:
    provider: cerebras
    model: llama-3.3-70b
defaults:
  model: llama-cerebras
roles: {}
`)
	parsed, err := ParseConfig(cfg)
	if err != nil {
		t.Fatalf("expected valid cerebras config, got: %v", err)
	}
	if parsed.Providers["cerebras"].Type != "cerebras" {
		t.Error("expected type cerebras")
	}
}

func TestGroqConfig_ResolveModel(t *testing.T) {
	cfg := []byte(`
providers:
  groq:
    type: groq
    base_url: https://api.groq.com/openai/v1
    api_key: test-key
    auth_type: bearer
models:
  mixtral-groq:
    provider: groq
    model: mixtral-8x7b-32768
defaults:
  model: mixtral-groq
roles:
  polecat:
    model: mixtral-groq
`)
	parsed, err := ParseConfig(cfg)
	if err != nil {
		t.Fatalf("ParseConfig failed: %v", err)
	}
	pc, model, err := parsed.ResolveModel("mixtral-groq")
	if err != nil {
		t.Fatalf("ResolveModel failed: %v", err)
	}
	if pc.Type != "groq" {
		t.Errorf("expected type groq, got %s", pc.Type)
	}
	if model != "mixtral-8x7b-32768" {
		t.Errorf("expected model mixtral-8x7b-32768, got %s", model)
	}
}
