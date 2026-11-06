// Package anthropic adapts the Claude Messages API to llm.Provider.
package anthropic

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/axonops/axai/internal/llm"
)

// DefaultModel is the model used when Config.Model is empty.
const DefaultModel = "claude-opus-5-5"

// Config configures the Anthropic provider.
type Config struct {
	APIKey     string
	Model      string // default DefaultModel
	BaseURL    string // optional, for proxies and tests
	MaxTokens  int    // default 16000
	HTTPClient *http.Client
}

var _ llm.Provider = (*Provider)(nil)

// Provider calls the Anthropic Messages API.
type Provider struct {
	client    sdk.Client
	model     string
	maxTokens int
}

// New returns an Anthropic provider.
func New(cfg Config) (*Provider, error) {
	if cfg.APIKey == "" {
		return nil, errors.New("anthropic: API key is required (set ANTHROPIC_API_KEY or AXAI_API_KEY)")
	}
	if cfg.Model == "" {
		cfg.Model = DefaultModel
	}
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = 16000
	}
	opts := []option.RequestOption{option.WithAPIKey(cfg.APIKey), option.WithMaxRetries(0)}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	if cfg.HTTPClient != nil {
		opts = append(opts, option.WithHTTPClient(cfg.HTTPClient))
	}
	return &Provider{client: sdk.NewClient(opts...), model: cfg.Model, maxTokens: cfg.MaxTokens}, nil
}

// Name returns the provider name.
func (p *Provider) Name() string { return "anthropic" }

// Capabilities reports what the provider supports.
func (p *Provider) Capabilities() llm.Caps { return llm.Caps{SchemaMode: llm.SchemaModeNative} }

// Complete sends one completion request.
func (p *Provider) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = p.maxTokens
	}
	params := sdk.MessageNewParams{Model: p.model, MaxTokens: int64(maxTokens)}
	if req.System != "" {
		params.System = []sdk.TextBlockParam{{Text: req.System, CacheControl: sdk.NewCacheControlEphemeralParam()}}
	}
	for _, m := range req.Messages {
		switch m.Role {
		case llm.RoleUser:
			params.Messages = append(params.Messages, sdk.NewUserMessage(sdk.NewTextBlock(m.Content)))
		case llm.RoleAssistant:
			params.Messages = append(params.Messages, sdk.NewAssistantMessage(sdk.NewTextBlock(m.Content)))
		default:
			return llm.Response{}, fmt.Errorf("anthropic: unsupported message role %q", m.Role)
		}
	}
	// Cache the prefix up to the last message: the fixer and structured
	// retries resend it with a longer tail.
	if n := len(params.Messages); n > 0 {
		if last := params.Messages[n-1].Content; len(last) > 0 && last[len(last)-1].OfText != nil {
			last[len(last)-1].OfText.CacheControl = sdk.NewCacheControlEphemeralParam()
		}
	}
	if req.Schema != nil {
		params.OutputConfig = sdk.OutputConfigParam{Format: sdk.JSONOutputFormatParam{Schema: req.Schema}}
	}

	// Streaming: the SDK refuses non-streaming calls whose max_tokens could
	// run past ten minutes.
	stream := p.client.Messages.NewStreaming(ctx, params)
	var msg sdk.Message
	for stream.Next() {
		if err := msg.Accumulate(stream.Current()); err != nil {
			_ = stream.Close()
			return llm.Response{}, fmt.Errorf("anthropic: %w", err)
		}
	}
	err := stream.Err()
	_ = stream.Close()
	if err != nil {
		var apiErr *sdk.Error
		if errors.As(err, &apiErr) {
			return llm.Response{}, &llm.StatusError{Provider: "anthropic", Code: apiErr.StatusCode, Body: apiErr.Error()}
		}
		return llm.Response{}, fmt.Errorf("anthropic: %w", err)
	}

	var sb strings.Builder
	for _, block := range msg.Content {
		if tb, ok := block.AsAny().(sdk.TextBlock); ok {
			sb.WriteString(tb.Text)
		}
	}
	out := llm.Response{
		Text:       sb.String(),
		StopReason: llm.StopEnd,
		Usage:      llm.Usage{InputTokens: int(msg.Usage.InputTokens), OutputTokens: int(msg.Usage.OutputTokens)},
	}
	switch msg.StopReason {
	case sdk.StopReasonMaxTokens:
		out.StopReason = llm.StopMaxTokens
	case sdk.StopReasonRefusal:
		out.StopReason = llm.StopRefusal
	}
	return out, nil
}
