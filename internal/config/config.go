// Package config loads provider settings from file, environment and flags.
// API keys are deliberately not configurable here; they come from the
// environment only.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ProviderConfig holds the settings of one model provider.
type ProviderConfig struct {
	Provider   string `yaml:"provider"`
	Model      string `yaml:"model"`
	BaseURL    string `yaml:"base_url"`
	SchemaMode string `yaml:"schema_mode"`
	// GenericKey allows AXAI_API_KEY_FILE and AXAI_API_KEY for this
	// provider. Only the top-level provider has it, so a stage that switches
	// to another provider never receives a key meant for the first one.
	GenericKey bool `yaml:"-"`
}

// Config is the loaded axai configuration.
type Config struct {
	ProviderConfig `yaml:",inline"`
	Stages         map[string]ProviderConfig `yaml:"stages"`
	CatalogueDir   string                    `yaml:"catalogue_dir"`
}

const defaultProvider = "anthropic"

// lookPath resolves the claude-cli binary when choosing the default
// provider; tests replace it.
var lookPath = exec.LookPath

// defaultFor picks claude-cli only when ANTHROPIC_API_KEY, AXAI_API_KEY and
// AXAI_API_KEY_FILE are all unset and the claude binary is installed;
// otherwise anthropic.
func defaultFor(getenv func(string) string) string {
	for _, k := range []string{"ANTHROPIC_API_KEY", "AXAI_API_KEY", "AXAI_API_KEY_FILE"} {
		if getenv(k) != "" {
			return defaultProvider
		}
	}
	bin := getenv("AXAI_CLAUDE_BIN")
	if bin == "" {
		bin = "claude"
	}
	if _, err := lookPath(bin); err == nil {
		return "claude-cli"
	}
	return defaultProvider
}

// DefaultPath returns the default config file location.
func DefaultPath(getenv func(string) string) string {
	base := getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(getenv("HOME"), ".config")
	}
	return filepath.Join(base, "axai", "config.yaml")
}

// Load reads path, or the default path when path is empty. A missing default
// file is not an error; a missing explicit file is.
func Load(path string, getenv func(string) string) (Config, error) {
	var c Config
	explicit := path != ""
	if !explicit {
		path = DefaultPath(getenv)
	}
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		dec := yaml.NewDecoder(bytes.NewReader(b))
		dec.KnownFields(true)
		if err := dec.Decode(&c); err != nil && !errors.Is(err, io.EOF) {
			return Config{}, fmt.Errorf("config %s: %w", path, err)
		}
	case errors.Is(err, fs.ErrNotExist) && !explicit:
	default:
		return Config{}, fmt.Errorf("config: %w", err)
	}
	overlay(&c.ProviderConfig, ProviderConfig{
		Provider:   getenv("AXAI_PROVIDER"),
		Model:      getenv("AXAI_MODEL"),
		BaseURL:    getenv("AXAI_BASE_URL"),
		SchemaMode: getenv("AXAI_SCHEMA_MODE"),
	})
	if c.Provider == "" {
		c.Provider = defaultFor(getenv)
	}
	c.GenericKey = true
	return c, nil
}

// Override applies command-line flags; empty fields are ignored.
func (c *Config) Override(flags ProviderConfig) { overlay(&c.ProviderConfig, flags) }

// ForStage returns the settings for one pipeline stage. A stage that names a
// different provider starts from a clean slate so the base model, URL and
// generic API key do not leak into it.
func (c Config) ForStage(stage string) ProviderConfig {
	pc := c.ProviderConfig
	s, ok := c.Stages[stage]
	if !ok {
		return pc
	}
	if s.Provider != "" && s.Provider != pc.Provider {
		pc = ProviderConfig{}
	}
	overlay(&pc, s)
	return pc
}

func overlay(dst *ProviderConfig, src ProviderConfig) {
	if src.Provider != "" {
		dst.Provider = src.Provider
	}
	if src.Model != "" {
		dst.Model = src.Model
	}
	if src.BaseURL != "" {
		dst.BaseURL = src.BaseURL
	}
	if src.SchemaMode != "" {
		dst.SchemaMode = src.SchemaMode
	}
}
