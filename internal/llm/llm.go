// Package llm defines the provider-neutral interface used by every stage that
// calls a language model.
package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"syscall"
)

// Role is a message author.
type Role string

const (
	// RoleSystem is the system role.
	RoleSystem Role = "system"
	// RoleUser is the user role.
	RoleUser Role = "user"
	// RoleAssistant is the assistant role.
	RoleAssistant Role = "assistant"
)

// Message is one chat message.
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
}

// Request is one completion call. A non-nil Schema asks the adapter to
// constrain output to that JSON schema where the provider supports it.
type Request struct {
	System     string         `json:"system,omitempty"`
	Messages   []Message      `json:"messages"`
	Schema     map[string]any `json:"schema,omitempty"`
	SchemaName string         `json:"schema_name,omitempty"`
	MaxTokens  int            `json:"max_tokens,omitempty"`
}

// StopReason says why a completion ended.
type StopReason string

const (
	// StopEnd is a normal end of output.
	StopEnd StopReason = "end"
	// StopMaxTokens means output was cut at the token limit.
	StopMaxTokens StopReason = "max_tokens"
	// StopRefusal means the model declined to answer.
	StopRefusal StopReason = "refusal"
)

// Usage counts tokens.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Response is one completion reply.
type Response struct {
	Text       string     `json:"text"`
	StopReason StopReason `json:"stop_reason"`
	Usage      Usage      `json:"usage"`
}

// SchemaMode is how an adapter asks the server for JSON output.
type SchemaMode string

const (
	// SchemaModeNative sends the JSON schema in the request.
	SchemaModeNative SchemaMode = "native"
	// SchemaModeJSONObject asks only for a JSON object.
	SchemaModeJSONObject SchemaMode = "json_object"
	// SchemaModePrompt puts the schema in the prompt only.
	SchemaModePrompt SchemaMode = "prompt"
)

// ParseSchemaMode parses a schema mode name.
func ParseSchemaMode(s string) (SchemaMode, error) {
	switch m := SchemaMode(s); m {
	case SchemaModeNative, SchemaModeJSONObject, SchemaModePrompt:
		return m, nil
	}
	return "", fmt.Errorf("unknown schema mode %q (want native, json_object or prompt)", s)
}

// Caps lists what a provider supports.
type Caps struct {
	SchemaMode SchemaMode
}

// Provider is a language-model backend.
type Provider interface {
	Name() string
	Complete(ctx context.Context, req Request) (Response, error)
	Capabilities() Caps
}

// StatusError is a non-2xx response from a provider API.
type StatusError struct {
	Provider string
	Code     int
	Body     string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("%s: HTTP %d: %s", e.Provider, e.Code, e.Body)
}

// IsTransient reports whether retrying the same request may succeed: a rate
// limit, a server error, a timeout, or a connection the server or a proxy
// dropped mid-request (long model calls hit idle limits).
func IsTransient(err error) bool {
	var se *StatusError
	if errors.As(err, &se) {
		return se.Code == 429 || se.Code >= 500
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) {
		return true
	}
	// io.EOF only counts when the HTTP transport returned it: the server
	// closed the connection before answering.
	var ue *url.Error
	return errors.As(err, &ue) && errors.Is(ue.Err, io.EOF)
}
