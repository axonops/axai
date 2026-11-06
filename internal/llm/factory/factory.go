// Package factory builds an llm.Provider from configuration.
package factory

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/axonops/axai/internal/config"
	"github.com/axonops/axai/internal/llm"
	"github.com/axonops/axai/internal/llm/anthropic"
	"github.com/axonops/axai/internal/llm/claudecli"
	"github.com/axonops/axai/internal/llm/openaicompat"
)

type openAIDefaults struct {
	baseURL        string
	model          string
	keyEnv         string
	keyRequired    bool
	schemaMode     llm.SchemaMode
	maxTokensField string
}

var openAIFamily = map[string]openAIDefaults{
	"openai":            {"https://api.openai.com/v1", "", "OPENAI_API_KEY", true, llm.SchemaModeNative, "max_completion_tokens"},
	"ollama":            {"http://localhost:11434/v1", "", "OLLAMA_API_KEY", false, llm.SchemaModeNative, "max_tokens"},
	"llamacpp":          {"http://localhost:8080/v1", "default", "LLAMACPP_API_KEY", false, llm.SchemaModeNative, "max_tokens"},
	"gemini":            {"https://generativelanguage.googleapis.com/v1beta/openai", "", "GEMINI_API_KEY", true, llm.SchemaModeNative, "max_tokens"},
	"openai-compatible": {"", "", "", false, llm.SchemaModePrompt, "max_tokens"},
}

const (
	retryAttempts = 3
	retryBase     = time.Second
)

// New returns a provider wrapped with retries for transient errors.
// AXAI_API_KEY_FILE and AXAI_API_KEY are used only when pc.GenericKey is
// set; otherwise only the provider's own variable is read.
func New(pc config.ProviderConfig, getenv func(string) string) (llm.Provider, error) {
	var fileKey string
	if pc.GenericKey {
		var err error
		if fileKey, err = readKeyFile(getenv("AXAI_API_KEY_FILE")); err != nil {
			return nil, err
		}
	}
	key := func(name string) string {
		if fileKey != "" {
			return fileKey
		}
		if pc.GenericKey {
			if k := getenv("AXAI_API_KEY"); k != "" {
				return k
			}
		}
		if name == "" {
			return ""
		}
		return getenv(name)
	}
	missing := func(name string) string {
		if pc.GenericKey {
			return fmt.Sprintf("set %s or AXAI_API_KEY", name)
		}
		return "set " + name
	}

	var p llm.Provider
	switch d, ok := openAIFamily[pc.Provider]; {
	case pc.Provider == "claude-cli":
		c, cerr := claudecli.New(claudecli.Config{Binary: getenv("AXAI_CLAUDE_BIN"), Model: pc.Model})
		if cerr != nil {
			return nil, cerr
		}
		p = c
	case pc.Provider == "anthropic":
		k := key("ANTHROPIC_API_KEY")
		if k == "" {
			return nil, errors.New("provider anthropic: " + missing("ANTHROPIC_API_KEY"))
		}
		a, aerr := anthropic.New(anthropic.Config{APIKey: k, Model: pc.Model, BaseURL: pc.BaseURL})
		if aerr != nil {
			return nil, aerr
		}
		p = a
	case ok:
		mode, merr := llm.ParseSchemaMode(or(pc.SchemaMode, string(d.schemaMode)))
		if merr != nil {
			return nil, fmt.Errorf("provider %s: %w", pc.Provider, merr)
		}
		cfg := openaicompat.Config{
			Name:           pc.Provider,
			BaseURL:        or(pc.BaseURL, d.baseURL),
			APIKey:         key(d.keyEnv),
			Model:          or(pc.Model, d.model),
			SchemaMode:     mode,
			MaxTokensField: d.maxTokensField,
		}
		if d.keyRequired && cfg.APIKey == "" {
			return nil, fmt.Errorf("provider %s: %s", pc.Provider, missing(d.keyEnv))
		}
		if cfg.BaseURL == "" {
			return nil, fmt.Errorf("provider %s: base_url is required", pc.Provider)
		}
		if cfg.Model == "" {
			return nil, fmt.Errorf("provider %s: model is required", pc.Provider)
		}
		o, oerr := openaicompat.New(cfg)
		if oerr != nil {
			return nil, oerr
		}
		p = o
	default:
		return nil, fmt.Errorf("unknown provider %q (supported: anthropic, claude-cli, openai, gemini, ollama, llamacpp, openai-compatible)", pc.Provider)
	}
	return llm.WithRetry(p, retryAttempts, retryBase), nil
}

// readKeyFile lets cloud-init pass the key as a file instead of an
// environment variable visible in user-data.
func readKeyFile(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("AXAI_API_KEY_FILE: %w", err)
	}
	k := strings.TrimSpace(string(b))
	if k == "" {
		return "", fmt.Errorf("AXAI_API_KEY_FILE: %s is empty", path)
	}
	return k, nil
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
