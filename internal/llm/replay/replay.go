// Package replay records provider responses to fixture files and replays them,
// so pipeline tests run offline and deterministically.
package replay

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/axonops/axai/internal/llm"
)

type fixture struct {
	Request  llm.Request  `json:"request"`
	Response llm.Response `json:"response"`
}

// Provider replays fixtures, or records them when it wraps an inner provider.
type Provider struct {
	dir   string
	inner llm.Provider
}

// New returns a provider that replays recorded calls.
func New(dir string) *Provider { return &Provider{dir: dir} }

// NewRecorder returns a provider that records the calls of p.
func NewRecorder(inner llm.Provider, dir string) *Provider {
	return &Provider{dir: dir, inner: inner}
}

// Key identifies a request. encoding/json sorts map keys, so the hash is stable.
func Key(req llm.Request) (string, error) {
	b, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:16], nil
}

// Name returns the provider name.
func (p *Provider) Name() string {
	if p.inner != nil {
		return p.inner.Name()
	}
	return "replay"
}

// Capabilities reports what the provider supports.
func (p *Provider) Capabilities() llm.Caps {
	if p.inner != nil {
		return p.inner.Capabilities()
	}
	return llm.Caps{SchemaMode: llm.SchemaModeNative}
}

// Complete sends one completion request.
func (p *Provider) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	key, err := Key(req)
	if err != nil {
		return llm.Response{}, fmt.Errorf("replay: key: %w", err)
	}
	path := filepath.Join(p.dir, key+".json")

	if p.inner == nil {
		b, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			return llm.Response{}, fmt.Errorf("replay: no fixture %s for request with system prompt %q", path, truncate(req.System, 80))
		}
		if err != nil {
			return llm.Response{}, fmt.Errorf("replay: %w", err)
		}
		var f fixture
		if err := json.Unmarshal(b, &f); err != nil {
			return llm.Response{}, fmt.Errorf("replay: decode %s: %w", path, err)
		}
		return f.Response, nil
	}

	resp, err := p.inner.Complete(ctx, req)
	if err != nil {
		return resp, err
	}
	if err := os.MkdirAll(p.dir, 0o750); err != nil {
		return resp, fmt.Errorf("replay: %w", err)
	}
	b, err := json.MarshalIndent(fixture{Request: req, Response: resp}, "", "  ")
	if err != nil {
		return resp, fmt.Errorf("replay: encode: %w", err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return resp, fmt.Errorf("replay: %w", err)
	}
	return resp, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
