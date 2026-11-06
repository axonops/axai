// Package openaicompat talks to any server that implements the OpenAI chat
// completions API: OpenAI, Ollama, llama.cpp, vLLM, LM Studio and others.
package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/axonops/axai/internal/llm"
)

// Config configures an OpenAI-compatible provider.
type Config struct {
	Name       string // reported by Name(); default "openai-compatible"
	BaseURL    string // including the /v1 suffix
	APIKey     string // optional for local servers
	Model      string
	SchemaMode llm.SchemaMode // default prompt
	// MaxTokensField is "max_tokens" (default) or "max_completion_tokens";
	// current OpenAI models reject the former.
	MaxTokensField string
	MaxTokens      int // default 8192
	HTTPClient     *http.Client
}

// Provider calls an OpenAI-compatible chat API.
type Provider struct{ cfg Config }

var _ llm.Provider = (*Provider)(nil)

// New returns an OpenAI-compatible provider.
func New(cfg Config) (*Provider, error) {
	if cfg.BaseURL == "" {
		return nil, errors.New("openaicompat: base URL is required")
	}
	if cfg.Model == "" {
		return nil, errors.New("openaicompat: model is required")
	}
	if cfg.Name == "" {
		cfg.Name = "openai-compatible"
	}
	if cfg.SchemaMode == "" {
		cfg.SchemaMode = llm.SchemaModePrompt
	}
	if cfg.MaxTokensField == "" {
		cfg.MaxTokensField = "max_tokens"
	}
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = 8192
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 10 * time.Minute}
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &Provider{cfg: cfg}, nil
}

// Name returns the provider name.
func (p *Provider) Name() string { return p.cfg.Name }

// Capabilities reports what the provider supports.
func (p *Provider) Capabilities() llm.Caps { return llm.Caps{SchemaMode: p.cfg.SchemaMode} }

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
			Refusal string `json:"refusal"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// Complete sends one completion request.
func (p *Provider) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	msgs := make([]chatMessage, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, chatMessage{Role: string(llm.RoleSystem), Content: req.System})
	}
	for _, m := range req.Messages {
		msgs = append(msgs, chatMessage{Role: string(m.Role), Content: m.Content})
	}
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = p.cfg.MaxTokens
	}
	body := map[string]any{
		"model":              p.cfg.Model,
		"messages":           msgs,
		p.cfg.MaxTokensField: maxTokens,
	}
	if rf := p.responseFormat(req); rf != nil {
		body["response_format"] = rf
	}
	b, err := json.Marshal(body)
	if err != nil {
		return llm.Response{}, fmt.Errorf("%s: encode request: %w", p.cfg.Name, err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.BaseURL+"/chat/completions", bytes.NewReader(b))
	if err != nil {
		return llm.Response{}, fmt.Errorf("%s: %w", p.cfg.Name, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if p.cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	}
	resp, err := p.cfg.HTTPClient.Do(httpReq)
	if err != nil {
		return llm.Response{}, fmt.Errorf("%s: %w", p.cfg.Name, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return llm.Response{}, fmt.Errorf("%s: read response: %w", p.cfg.Name, err)
	}
	if resp.StatusCode/100 != 2 {
		return llm.Response{}, &llm.StatusError{Provider: p.cfg.Name, Code: resp.StatusCode, Body: truncate(string(data), 2048)}
	}

	var cr chatResponse
	if err := json.Unmarshal(data, &cr); err != nil {
		return llm.Response{}, fmt.Errorf("%s: decode response: %w", p.cfg.Name, err)
	}
	if len(cr.Choices) == 0 {
		return llm.Response{}, fmt.Errorf("%s: response has no choices", p.cfg.Name)
	}
	c := cr.Choices[0]
	out := llm.Response{
		Text:       c.Message.Content,
		StopReason: llm.StopEnd,
		Usage:      llm.Usage{InputTokens: cr.Usage.PromptTokens, OutputTokens: cr.Usage.CompletionTokens},
	}
	switch c.FinishReason {
	case "length":
		out.StopReason = llm.StopMaxTokens
	case "content_filter":
		out.StopReason = llm.StopRefusal
	}
	if c.Message.Refusal != "" {
		out.StopReason = llm.StopRefusal
		out.Text = c.Message.Refusal
	}
	return out, nil
}

func (p *Provider) responseFormat(req llm.Request) map[string]any {
	if req.Schema == nil {
		return nil
	}
	switch p.cfg.SchemaMode {
	case llm.SchemaModeNative:
		name := req.SchemaName
		if name == "" {
			name = "output"
		}
		return map[string]any{
			"type":        "json_schema",
			"json_schema": map[string]any{"name": name, "schema": req.Schema},
		}
	case llm.SchemaModeJSONObject:
		return map[string]any{"type": "json_object"}
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
