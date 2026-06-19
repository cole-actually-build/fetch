package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultPopulatesRoles(t *testing.T) {
	c := Default()
	if c.ModelFor(RoleExtract) == "" {
		t.Fatal("expected a default extract model")
	}
	if c.Ollama.BaseURL == "" {
		t.Fatal("expected default ollama base url")
	}
	if c.Search.MaxResults <= 0 {
		t.Fatal("expected positive default max results")
	}
}

func TestLoadOverlaysOntoDefaults(t *testing.T) {
	c, err := Load(filepath.Join("testdata", "config.toml"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// Overridden by file:
	if c.Ollama.BaseURL != "http://localhost:9999" {
		t.Fatalf("base url not overridden: %q", c.Ollama.BaseURL)
	}
	if c.Search.MaxResults != 8 {
		t.Fatalf("max results not overridden: %d", c.Search.MaxResults)
	}
	if c.ModelFor(RoleInterview) != "gpt-oss:20b" {
		t.Fatalf("interview model: %q", c.ModelFor(RoleInterview))
	}
	// Not in file → still defaulted:
	if c.ModelFor(RoleReplan) == "" {
		t.Fatal("replan role should fall back to a default")
	}
}

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	c, err := Load(filepath.Join("testdata", "does-not-exist.toml"))
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if c.Ollama.BaseURL == "" {
		t.Fatal("expected defaults for missing file")
	}
}

func TestAPIKeyReadsEnv(t *testing.T) {
	c := Default()
	c.Search.APIKeyEnv = "FETCH_TEST_KEY"
	os.Setenv("FETCH_TEST_KEY", "secret")
	defer os.Unsetenv("FETCH_TEST_KEY")
	if c.APIKey() != "secret" {
		t.Fatalf("api key: %q", c.APIKey())
	}
}
