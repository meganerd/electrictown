package provider

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config represents the top-level electrictown configuration.
type Config struct {
	// Providers defines available LLM providers and their connection details.
	Providers map[string]ProviderConfig `yaml:"providers"`

	// Models defines model aliases that map to provider + model pairs.
	Models map[string]ModelConfig `yaml:"models"`

	// Roles maps agent roles (mayor, polecat, crew, etc.) to model aliases.
	Roles map[string]RoleConfig `yaml:"roles"`

	// Defaults sets fallback values when not specified per-role.
	Defaults DefaultsConfig `yaml:"defaults"`
}

// AuthType constants for provider authentication methods.
const (
	AuthNone   = "none"   // No authentication (default for local Ollama)
	AuthBearer = "bearer" // Bearer token (default when api_key is set)
	AuthBasic  = "basic"  // HTTP Basic Auth (for reverse-proxied Ollama)
)

// ProviderConfig defines connection details for a single provider.
type ProviderConfig struct {
	Type     string `yaml:"type"`               // "openai", "anthropic", "ollama"
	BaseURL  string `yaml:"base_url"`           // API base URL
	APIKey   string `yaml:"api_key,omitempty"`  // API key (or env var reference)
	AuthType string `yaml:"auth_type,omitempty"` // "bearer" (default), "basic", "none"
	Org      string `yaml:"org,omitempty"`      // Organization ID (OpenAI)
}

// ModelConfig maps a model alias to a specific provider and model name.
type ModelConfig struct {
	Provider string `yaml:"provider"` // key into Providers map
	Model    string `yaml:"model"`    // actual model ID at the provider
}

// RoleConfig defines which model(s) a given agent role should use.
type RoleConfig struct {
	Model     string   `yaml:"model"`               // primary model alias
	Pool      []string `yaml:"pool,omitempty"`       // parallel worker pool model aliases
	Fallbacks []string `yaml:"fallbacks,omitempty"`  // fallback model aliases in order
}

// DefaultsConfig provides fallback settings.
type DefaultsConfig struct {
	Model       string   `yaml:"model"`                 // default model alias
	Fallbacks   []string `yaml:"fallbacks,omitempty"`    // default fallback chain
	MaxTokens   int      `yaml:"max_tokens,omitempty"`   // default max tokens
	Temperature float64  `yaml:"temperature,omitempty"`  // default temperature
	LogDir      string   `yaml:"log_dir,omitempty"`      // directory for run logs (default: ~/Documents)
}

// LoadConfig reads and parses an electrictown YAML config file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}
	return ParseConfig(data)
}

// ParseConfig parses YAML bytes into a Config.
func ParseConfig(data []byte) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	// Resolve environment variable references in API keys.
	for name, p := range cfg.Providers {
		if len(p.APIKey) > 0 && p.APIKey[0] == '$' {
			varName := p.APIKey[1:]
			p.APIKey = os.Getenv(varName)
			// Fail early for bearer auth with an unset env var — the request will
			// always be rejected without it. Basic auth defers validation to runtime
			// (colon format can't be checked until the value is actually resolved).
			if p.APIKey == "" && p.AuthType == AuthBearer {
				return nil, fmt.Errorf("provider %q requires an API key but $%s is not set or is empty", name, varName)
			}
			cfg.Providers[name] = p
		}
	}
	return &cfg, nil
}

// Validate checks the config for internal consistency.
func (c *Config) Validate() error {
	if len(c.Providers) == 0 {
		return fmt.Errorf("config: no providers defined")
	}
	// Validate model references.
	for alias, mc := range c.Models {
		if _, ok := c.Providers[mc.Provider]; !ok {
			return fmt.Errorf("config: model %q references unknown provider %q", alias, mc.Provider)
		}
		if mc.Model == "" {
			return fmt.Errorf("config: model %q has empty model name", alias)
		}
	}
	// Validate role references.
	for role, rc := range c.Roles {
		if _, ok := c.Models[rc.Model]; !ok {
			return fmt.Errorf("config: role %q references unknown model alias %q", role, rc.Model)
		}
		for _, fb := range rc.Fallbacks {
			if _, ok := c.Models[fb]; !ok {
				return fmt.Errorf("config: role %q fallback references unknown model alias %q", role, fb)
			}
		}
		for _, pa := range rc.Pool {
			if _, ok := c.Models[pa]; !ok {
				return fmt.Errorf("config: role %q pool references unknown model alias %q", role, pa)
			}
		}
	}
	// Validate defaults.
	if c.Defaults.Model != "" {
		if _, ok := c.Models[c.Defaults.Model]; !ok {
			return fmt.Errorf("config: defaults reference unknown model alias %q", c.Defaults.Model)
		}
	}
	for _, fb := range c.Defaults.Fallbacks {
		if _, ok := c.Models[fb]; !ok {
			return fmt.Errorf("config: defaults fallback references unknown model alias %q", fb)
		}
	}
	// Warn on empty provider type and validate auth_type.
	for name, pc := range c.Providers {
		if pc.Type == "" {
			return fmt.Errorf("config: provider %q has empty type", name)
		}
		// Validate auth_type if specified.
		switch pc.AuthType {
		case "", AuthBearer, AuthBasic, AuthNone:
			// valid
		default:
			return fmt.Errorf("config: provider %q has invalid auth_type %q (must be bearer, basic, or none)", name, pc.AuthType)
		}
		if pc.AuthType == AuthBasic && pc.APIKey != "" && len(pc.APIKey) > 0 && pc.APIKey[0] != '$' {
			if !strings.Contains(pc.APIKey, ":") {
				return fmt.Errorf("config: provider %q auth_type is basic but api_key does not contain ':' (expected user:password)", name)
			}
		}
		if (pc.AuthType == AuthBearer || pc.AuthType == AuthBasic) && pc.APIKey == "" {
			return fmt.Errorf("config: provider %q auth_type is %q but no api_key is set", name, pc.AuthType)
		}
	}
	// Detect pointless fallbacks (same provider+model as primary).
	for role, rc := range c.Roles {
		primary, ok := c.Models[rc.Model]
		if !ok {
			continue // already caught above
		}
		for _, fb := range rc.Fallbacks {
			fbModel, ok := c.Models[fb]
			if !ok {
				continue // already caught above
			}
			if primary.Provider == fbModel.Provider && primary.Model == fbModel.Model {
				return fmt.Errorf("config: role %q fallback %q resolves to same provider+model as primary %q", role, fb, rc.Model)
			}
		}
	}
	return nil
}

// ResolveLogDir returns the configured log directory, expanding ~ and falling
// back to ~/Documents when no log_dir is set in the config.
func (c *Config) ResolveLogDir() (string, error) {
	dir := c.Defaults.LogDir
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot determine home directory: %w", err)
		}
		return filepath.Join(home, "Documents"), nil
	}
	if len(dir) >= 2 && dir[:2] == "~/" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot determine home directory: %w", err)
		}
		return filepath.Join(home, dir[2:]), nil
	}
	return dir, nil
}

// ResolveRole returns the provider config and model name for a given role.
// Falls back to defaults if the role is not explicitly configured.
func (c *Config) ResolveRole(role string) (ProviderConfig, string, error) {
	rc, ok := c.Roles[role]
	if !ok {
		if c.Defaults.Model == "" {
			return ProviderConfig{}, "", fmt.Errorf("config: role %q not configured and no default set", role)
		}
		rc = RoleConfig{Model: c.Defaults.Model, Fallbacks: c.Defaults.Fallbacks}
	}
	return c.ResolveModel(rc.Model)
}

// ResolveModel returns the provider config and actual model name for a model alias.
func (c *Config) ResolveModel(alias string) (ProviderConfig, string, error) {
	mc, ok := c.Models[alias]
	if !ok {
		return ProviderConfig{}, "", fmt.Errorf("config: unknown model alias %q", alias)
	}
	pc, ok := c.Providers[mc.Provider]
	if !ok {
		return ProviderConfig{}, "", fmt.Errorf("config: model %q references unknown provider %q", alias, mc.Provider)
	}
	return pc, mc.Model, nil
}

// FallbacksForRole returns the ordered fallback model aliases for a role.
func (c *Config) FallbacksForRole(role string) []string {
	if rc, ok := c.Roles[role]; ok {
		return rc.Fallbacks
	}
	return c.Defaults.Fallbacks
}

// PoolForRole returns the pool model aliases for a role, or nil if no pool is configured.
func (c *Config) PoolForRole(role string) []string {
	if rc, ok := c.Roles[role]; ok {
		return rc.Pool
	}
	return nil
}
