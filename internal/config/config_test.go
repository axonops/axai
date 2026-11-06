package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/axonops/axai/internal/config"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	return p
}

func TestPrecedence(t *testing.T) {
	path := write(t, "provider: ollama\nmodel: qwen3:14b\nbase_url: http://gpu:11434/v1\n")
	c, err := config.Load(path, env(map[string]string{"AXAI_MODEL": "llama4"}))
	require.NoError(t, err)
	require.Equal(t, config.ProviderConfig{Provider: "ollama", Model: "llama4", BaseURL: "http://gpu:11434/v1", GenericKey: true}, c.ProviderConfig)

	c.Override(config.ProviderConfig{Model: "from-flag"})
	require.Equal(t, "from-flag", c.Model)
	require.Equal(t, "ollama", c.Provider)
	require.True(t, c.GenericKey)
}

func stubLookPath(t *testing.T, found bool) {
	t.Helper()
	t.Cleanup(config.SetLookPath(func(string) (string, error) {
		if found {
			return "/usr/bin/claude", nil
		}
		return "", os.ErrNotExist
	}))
}

func TestDefaultFallsBackToClaudeCLI(t *testing.T) {
	dir := t.TempDir()
	load := func(m map[string]string) string {
		m["XDG_CONFIG_HOME"] = dir
		c, err := config.Load("", env(m))
		require.NoError(t, err)
		return c.Provider
	}
	stubLookPath(t, true)
	require.Equal(t, "claude-cli", load(map[string]string{}))
	require.Equal(t, "anthropic", load(map[string]string{"ANTHROPIC_API_KEY": "k"}))
	require.Equal(t, "anthropic", load(map[string]string{"AXAI_API_KEY": "k"}))
	require.Equal(t, "ollama", load(map[string]string{"AXAI_PROVIDER": "ollama"}))
	stubLookPath(t, false)
	require.Equal(t, "anthropic", load(map[string]string{}))
}

func TestDefaults(t *testing.T) {
	stubLookPath(t, false)
	dir := t.TempDir()
	c, err := config.Load("", env(map[string]string{"XDG_CONFIG_HOME": dir}))
	require.NoError(t, err)
	require.Equal(t, "anthropic", c.Provider)
	require.Equal(t, filepath.Join(dir, "axai", "config.yaml"), config.DefaultPath(env(map[string]string{"XDG_CONFIG_HOME": dir})))
	require.Equal(t, "/home/u/.config/axai/config.yaml", config.DefaultPath(env(map[string]string{"HOME": "/home/u"})))
}

func TestForStage(t *testing.T) {
	path := write(t, `
provider: anthropic
model: claude-opus-5-5
stages:
  fixer:
    provider: ollama
    model: qwen3:8b
  architect:
    model: claude-fable-5-1
`)
	c, err := config.Load(path, env(nil))
	require.NoError(t, err)
	// A stage on another provider must not receive the top-level AXAI_* key.
	require.Equal(t, config.ProviderConfig{Provider: "ollama", Model: "qwen3:8b"}, c.ForStage("fixer"))
	require.Equal(t, config.ProviderConfig{Provider: "anthropic", Model: "claude-fable-5-1", GenericKey: true}, c.ForStage("architect"))
	require.True(t, c.ForStage("intake").GenericKey)
	require.Equal(t, c.ProviderConfig, c.ForStage("intake"))
}

func TestLoadCatalogueDirFromFile(t *testing.T) {
	path := write(t, "provider: anthropic\ncatalogue_dir: /x\n")
	c, err := config.Load(path, env(nil))
	require.NoError(t, err)
	require.Equal(t, "/x", c.CatalogueDir)
}

func TestOverrideCatalogueDir(t *testing.T) {
	path := write(t, "provider: anthropic\ncatalogue_dir: /from-file\n")
	c, err := config.Load(path, env(nil))
	require.NoError(t, err)
	c.CatalogueDir = "/from-flag"
	require.Equal(t, "/from-flag", c.CatalogueDir)
}

func TestLoadErrors(t *testing.T) {
	_, err := config.Load(filepath.Join(t.TempDir(), "missing.yaml"), env(nil))
	require.ErrorContains(t, err, "missing.yaml")

	_, err = config.Load(write(t, "provider: openai\napi_key: sk-123\n"), env(nil))
	require.ErrorContains(t, err, "api_key")
}
