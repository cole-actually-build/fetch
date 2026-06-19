// Package config loads fetch's runtime configuration: where data lives,
// which Ollama model serves each role, and provider endpoints/keys.
package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Roles map units of agent work to models.
const (
	RoleInterview = "interview"
	RolePlan      = "plan"
	RoleSchema    = "schema"
	RoleExtract   = "extract"
	RoleReplan    = "replan"
	RoleRoute     = "route"
)

type Config struct {
	DataDir string            `toml:"data_dir"`
	Models  map[string]string `toml:"models"`
	Ollama  OllamaConfig      `toml:"ollama"`
	Search  SearchConfig      `toml:"search"`
	Fetch   FetchConfig       `toml:"fetch"`
}

type OllamaConfig struct {
	BaseURL string `toml:"base_url"`
}

type SearchConfig struct {
	Provider   string `toml:"provider"`
	BaseURL    string `toml:"base_url"`
	APIKeyEnv  string `toml:"api_key_env"`
	MaxResults int    `toml:"max_results"`
}

type FetchConfig struct {
	UserAgent      string `toml:"user_agent"`
	TimeoutSeconds int    `toml:"timeout_seconds"`
	MaxBytes       int64  `toml:"max_bytes"`
}

// Default returns a fully-populated config. Load overlays a file onto this.
func Default() Config {
	return Config{
		DataDir: "~/.local/share/fetch",
		Models: map[string]string{
			RoleInterview: "gpt-oss:20b",
			RolePlan:      "gpt-oss:20b",
			RoleSchema:    "qwen3-coder:latest",
			RoleExtract:   "qwen3-coder:latest",
			RoleReplan:    "gpt-oss:20b",
			RoleRoute:     "llama3.2:latest",
		},
		Ollama: OllamaConfig{BaseURL: "http://localhost:11434"},
		Search: SearchConfig{
			Provider:   "tavily",
			BaseURL:    "https://api.tavily.com",
			APIKeyEnv:  "TAVILY_API_KEY",
			MaxResults: 5,
		},
		Fetch: FetchConfig{
			UserAgent:      "fetch/0.1 (+https://github.com/cole/fetch)",
			TimeoutSeconds: 30,
			MaxBytes:       5 << 20, // 5 MiB
		},
	}
}

// Load starts from Default and decodes the TOML file at path over it.
// A missing file is not an error: defaults are returned.
func Load(path string) (Config, error) {
	c := Default()
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return expand(c), nil
		}
		return c, err
	}
	if _, err := toml.DecodeFile(path, &c); err != nil {
		return c, err
	}
	// A models table in the file replaces individual keys it sets but should
	// not wipe roles it omits. Merge file values over defaults.
	merged := Default().Models
	for k, v := range c.Models {
		merged[k] = v
	}
	c.Models = merged
	return expand(c), nil
}

func expand(c Config) Config {
	if strings.HasPrefix(c.DataDir, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			c.DataDir = filepath.Join(home, strings.TrimPrefix(c.DataDir, "~"))
		}
	}
	return c
}

// ModelFor returns the model assigned to a role, falling back to the route
// model and then a final hard default so callers never get an empty string.
func (c Config) ModelFor(role string) string {
	if m, ok := c.Models[role]; ok && m != "" {
		return m
	}
	if m, ok := c.Models[RoleRoute]; ok && m != "" {
		return m
	}
	return "llama3.2:latest"
}

// APIKey reads the search provider's API key from the configured env var.
func (c Config) APIKey() string {
	if c.Search.APIKeyEnv == "" {
		return ""
	}
	return os.Getenv(c.Search.APIKeyEnv)
}
