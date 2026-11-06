// Package claudecli adapts the local `claude` binary to llm.Provider. The
// CLI handles its own authentication, so no API key is needed.
package claudecli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/axonops/axai/internal/llm"
)

// DefaultBinary is looked up on PATH when Config.Binary is empty.
const DefaultBinary = "claude"

// Config selects the binary and model to run.
type Config struct {
	Binary string // default DefaultBinary
	Model  string // optional; empty uses the CLI's own default
}

var _ llm.Provider = (*Provider)(nil)

// Provider runs one `claude -p` subprocess per completion.
type Provider struct {
	bin   string
	model string
}

// New resolves the binary now so a missing CLI fails at start-up rather than
// on the first completion.
func New(cfg Config) (*Provider, error) {
	if cfg.Binary == "" {
		cfg.Binary = DefaultBinary
	}
	path, err := exec.LookPath(cfg.Binary)
	if err != nil {
		return nil, fmt.Errorf("provider claude-cli: %q not found on PATH (install Claude Code and run `claude` once to sign in): %w", cfg.Binary, err)
	}
	return &Provider{bin: path, model: cfg.Model}, nil
}

// Name reports the provider name used in config and errors.
func (p *Provider) Name() string { return "claude-cli" }

// Capabilities reports native schema support through --json-schema.
func (p *Provider) Capabilities() llm.Caps { return llm.Caps{SchemaMode: llm.SchemaModeNative} }

type cliResult struct {
	Type           string          `json:"type"`
	IsError        bool            `json:"is_error"`
	Result         string          `json:"result"`
	StopReason     string          `json:"stop_reason"`
	APIErrorStatus *int            `json:"api_error_status"`
	Structured     json.RawMessage `json:"structured_output"`
	Usage          struct {
		InputTokens         int `json:"input_tokens"`
		CacheCreationTokens int `json:"cache_creation_input_tokens"`
		CacheReadTokens     int `json:"cache_read_input_tokens"`
		OutputTokens        int `json:"output_tokens"`
	} `json:"usage"`
}

// Complete runs one completion through the claude binary.
func (p *Provider) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	args := []string{
		"-p", "--output-format", "json",
		"--tools", "", // pure completion: no file or shell access
		"--no-session-persistence",
		"--setting-sources", "", // ignore user hooks and project CLAUDE.md
	}
	if p.model != "" {
		args = append(args, "--model", p.model)
	}
	if req.System != "" {
		args = append(args, "--system-prompt", req.System)
	}
	if req.Schema != nil {
		sb, err := json.Marshal(req.Schema)
		if err != nil {
			return llm.Response{}, fmt.Errorf("claude-cli: encode schema: %w", err)
		}
		args = append(args, "--json-schema", string(sb))
	}
	prompt, err := transcript(req.Messages)
	if err != nil {
		return llm.Response{}, err
	}

	cmd := exec.CommandContext(ctx, p.bin, args...)
	cmd.Stdin = strings.NewReader(prompt)
	// Bound the wait for output pipes after a cancel kills the process.
	cmd.WaitDelay = 5 * time.Second
	if req.MaxTokens > 0 {
		cmd.Env = append(os.Environ(), "CLAUDE_CODE_MAX_OUTPUT_TOKENS="+strconv.Itoa(req.MaxTokens))
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()

	var r cliResult
	decErr := json.Unmarshal(stdout.Bytes(), &r)
	if decErr == nil && r.IsError {
		msg := strings.TrimSpace(r.Result)
		if r.APIErrorStatus != nil {
			return llm.Response{}, &llm.StatusError{Provider: "claude-cli", Code: *r.APIErrorStatus, Body: msg}
		}
		return llm.Response{}, fmt.Errorf("claude-cli: %s", msg)
	}
	if ctx.Err() != nil {
		return llm.Response{}, fmt.Errorf("claude-cli: %w", ctx.Err())
	}
	if runErr != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(stdout.String())
		}
		return llm.Response{}, fmt.Errorf("claude-cli: %w: %s", runErr, truncate(detail, 2048))
	}
	if decErr != nil {
		return llm.Response{}, fmt.Errorf("claude-cli: decode output: %w", decErr)
	}

	text := r.Result
	if req.Schema != nil && len(r.Structured) > 0 && string(r.Structured) != "null" {
		text = string(r.Structured)
	}
	out := llm.Response{
		Text:       text,
		StopReason: llm.StopEnd,
		Usage: llm.Usage{
			InputTokens:  r.Usage.InputTokens + r.Usage.CacheCreationTokens + r.Usage.CacheReadTokens,
			OutputTokens: r.Usage.OutputTokens,
		},
	}
	switch r.StopReason {
	case "max_tokens":
		out.StopReason = llm.StopMaxTokens
	case "refusal":
		out.StopReason = llm.StopRefusal
	}
	return out, nil
}

// transcript flattens the conversation into the single prompt `claude -p`
// reads. A lone user message is passed through unchanged.
func transcript(msgs []llm.Message) (string, error) {
	if len(msgs) == 0 {
		return "", errors.New("claude-cli: no messages")
	}
	if len(msgs) == 1 && msgs[0].Role == llm.RoleUser {
		return msgs[0].Content, nil
	}
	var sb strings.Builder
	for _, m := range msgs {
		switch m.Role {
		case llm.RoleUser:
			sb.WriteString("Human: ")
		case llm.RoleAssistant:
			sb.WriteString("Assistant: ")
		default:
			return "", fmt.Errorf("claude-cli: unsupported message role %q", m.Role)
		}
		sb.WriteString(m.Content)
		sb.WriteString("\n\n")
	}
	sb.WriteString("Assistant:")
	return sb.String(), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "..."
}
