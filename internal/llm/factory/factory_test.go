package factory_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/axonops/axai/internal/config"
	"github.com/axonops/axai/internal/llm"
	"github.com/axonops/axai/internal/llm/factory"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestUnknownProvider(t *testing.T) {
	_, err := factory.New(config.ProviderConfig{Provider: "foo"}, env(nil))
	require.ErrorContains(t, err, `unknown provider "foo"`)
}

func TestMissingKeys(t *testing.T) {
	_, err := factory.New(config.ProviderConfig{Provider: "anthropic"}, env(nil))
	require.ErrorContains(t, err, "ANTHROPIC_API_KEY")
	_, err = factory.New(config.ProviderConfig{Provider: "openai", Model: "gpt-x"}, env(nil))
	require.ErrorContains(t, err, "OPENAI_API_KEY")
}

func TestMissingModelAndURL(t *testing.T) {
	_, err := factory.New(config.ProviderConfig{Provider: "ollama"}, env(nil))
	require.ErrorContains(t, err, "model is required")
	_, err = factory.New(config.ProviderConfig{Provider: "openai-compatible", Model: "m"}, env(nil))
	require.ErrorContains(t, err, "base_url is required")
}

func TestInvalidSchemaMode(t *testing.T) {
	_, err := factory.New(config.ProviderConfig{Provider: "ollama", Model: "m", SchemaMode: "xml"}, env(nil))
	require.ErrorContains(t, err, "unknown schema mode")
}

func TestAnthropicNames(t *testing.T) {
	p, err := factory.New(config.ProviderConfig{Provider: "anthropic"}, env(map[string]string{"ANTHROPIC_API_KEY": "k"}))
	require.NoError(t, err)
	require.Equal(t, "anthropic", p.Name())
	require.Equal(t, llm.SchemaModeNative, p.Capabilities().SchemaMode)
}

func TestAxaiKeyOverridesAndDefaults(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		assert.Contains(t, string(b), `"max_completion_tokens"`)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"OK"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	p, err := factory.New(
		config.ProviderConfig{Provider: "openai", Model: "gpt-x", BaseURL: srv.URL, GenericKey: true},
		env(map[string]string{"OPENAI_API_KEY": "plain", "AXAI_API_KEY": "override"}),
	)
	require.NoError(t, err)
	require.Equal(t, "openai", p.Name())
	require.Equal(t, llm.SchemaModeNative, p.Capabilities().SchemaMode)
	_, err = p.Complete(context.Background(), llm.Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}}})
	require.NoError(t, err)
	require.Equal(t, "Bearer override", auth)
}

func TestAPIKeyFile(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"OK"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "key")
	require.NoError(t, os.WriteFile(path, []byte("from-file\n"), 0o600))
	pc := config.ProviderConfig{Provider: "openai", Model: "gpt-x", BaseURL: srv.URL, GenericKey: true}

	p, err := factory.New(pc, env(map[string]string{"AXAI_API_KEY_FILE": path, "AXAI_API_KEY": "from-env"}))
	require.NoError(t, err)
	_, err = p.Complete(context.Background(), llm.Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}}})
	require.NoError(t, err)
	require.Equal(t, "Bearer from-file", auth)

	_, err = factory.New(pc, env(map[string]string{"AXAI_API_KEY_FILE": filepath.Join(t.TempDir(), "missing")}))
	require.ErrorContains(t, err, "AXAI_API_KEY_FILE")

	empty := filepath.Join(t.TempDir(), "empty")
	require.NoError(t, os.WriteFile(empty, []byte("\n"), 0o600))
	_, err = factory.New(pc, env(map[string]string{"AXAI_API_KEY_FILE": empty}))
	require.ErrorContains(t, err, "is empty")
}

func TestOpenAICompatibleDefaultsToPromptMode(t *testing.T) {
	p, err := factory.New(config.ProviderConfig{Provider: "openai-compatible", Model: "m", BaseURL: "http://x/v1"}, env(nil))
	require.NoError(t, err)
	require.Equal(t, llm.SchemaModePrompt, p.Capabilities().SchemaMode)
}

func TestOpenAICompatibleNeverSendsOpenAIKey(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"OK"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	p, err := factory.New(
		config.ProviderConfig{Provider: "openai-compatible", Model: "m", BaseURL: srv.URL},
		env(map[string]string{"OPENAI_API_KEY": "leak"}),
	)
	require.NoError(t, err)
	_, err = p.Complete(context.Background(), llm.Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}}})
	require.NoError(t, err)
	assert.Empty(t, auth)
}

func TestStageProviderIgnoresGenericKey(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"OK"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	keyFile := filepath.Join(t.TempDir(), "key")
	require.NoError(t, os.WriteFile(keyFile, []byte("file-key\n"), 0o600))
	vars := map[string]string{"AXAI_API_KEY": "anthropic-key", "AXAI_API_KEY_FILE": keyFile}

	p, err := factory.New(config.ProviderConfig{Provider: "ollama", Model: "m", BaseURL: srv.URL}, env(vars))
	require.NoError(t, err)
	_, err = p.Complete(context.Background(), llm.Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}}})
	require.NoError(t, err)
	assert.Empty(t, auth)

	vars["OLLAMA_API_KEY"] = "ollama-key"
	p, err = factory.New(config.ProviderConfig{Provider: "ollama", Model: "m", BaseURL: srv.URL}, env(vars))
	require.NoError(t, err)
	_, err = p.Complete(context.Background(), llm.Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}}})
	require.NoError(t, err)
	require.Equal(t, "Bearer ollama-key", auth)

	_, err = factory.New(config.ProviderConfig{Provider: "openai", Model: "gpt-x"}, env(vars))
	require.ErrorContains(t, err, "set OPENAI_API_KEY")
	require.NotContains(t, err.Error(), "AXAI_API_KEY")
}

func TestGenericKeyErrorMentionsAxaiKey(t *testing.T) {
	_, err := factory.New(config.ProviderConfig{Provider: "anthropic", GenericKey: true}, env(nil))
	require.ErrorContains(t, err, "set ANTHROPIC_API_KEY or AXAI_API_KEY")
}

func TestGemini(t *testing.T) {
	_, err := factory.New(config.ProviderConfig{Provider: "gemini", Model: "gemini-3.8-flash"}, env(nil))
	require.ErrorContains(t, err, "GEMINI_API_KEY")

	// No base_url: the Google endpoint is the default.
	p, err := factory.New(config.ProviderConfig{Provider: "gemini", Model: "gemini-3.8-flash"}, env(map[string]string{"GEMINI_API_KEY": "g"}))
	require.NoError(t, err)
	require.Equal(t, "gemini", p.Name())
	require.Equal(t, llm.SchemaModeNative, p.Capabilities().SchemaMode)

	var auth string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		assert.NoError(t, json.Unmarshal(b, &body))
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"{}"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	p, err = factory.New(config.ProviderConfig{Provider: "gemini", Model: "gemini-3.8-flash", BaseURL: srv.URL}, env(map[string]string{"GEMINI_API_KEY": "g"}))
	require.NoError(t, err)
	_, err = p.Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}},
		Schema:   map[string]any{"type": "object"},
	})
	require.NoError(t, err)
	require.Equal(t, "Bearer g", auth)
	require.Contains(t, body, "max_tokens")
	require.Equal(t, "json_schema", body["response_format"].(map[string]any)["type"])
}

func TestClaudeCLI(t *testing.T) {
	_, err := factory.New(config.ProviderConfig{Provider: "claude-cli"}, env(map[string]string{"AXAI_CLAUDE_BIN": "no-such-claude-binary"}))
	require.ErrorContains(t, err, "not found on PATH")

	bin := filepath.Join(t.TempDir(), "claude")
	require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755))
	p, err := factory.New(config.ProviderConfig{Provider: "claude-cli"}, env(map[string]string{"AXAI_CLAUDE_BIN": bin}))
	require.NoError(t, err)
	require.Equal(t, "claude-cli", p.Name())
	require.Equal(t, llm.SchemaModeNative, p.Capabilities().SchemaMode)
}
