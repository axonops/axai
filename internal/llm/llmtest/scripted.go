// Package llmtest provides an in-memory provider for unit tests.
package llmtest

import (
	"context"
	"fmt"
	"sync"

	"github.com/axonops/axai/internal/llm"
)

// Step is one scripted reply.
type Step struct {
	Response llm.Response
	Err      error
}

// Text is a step that returns s with a normal stop.
func Text(s string) Step {
	return Step{Response: llm.Response{Text: s, StopReason: llm.StopEnd}}
}

// Scripted returns Steps in order and records every request in Calls.
type Scripted struct {
	ProviderName string
	Caps         llm.Caps
	Steps        []Step
	Calls        []llm.Request

	mu sync.Mutex
}

// Name returns the provider name.
func (s *Scripted) Name() string {
	if s.ProviderName == "" {
		return "scripted"
	}
	return s.ProviderName
}

// Capabilities reports the scripted capabilities.
func (s *Scripted) Capabilities() llm.Caps {
	if s.Caps.SchemaMode == "" {
		return llm.Caps{SchemaMode: llm.SchemaModeNative}
	}
	return s.Caps
}

// Complete returns the next scripted step.
func (s *Scripted) Complete(_ context.Context, req llm.Request) (llm.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Copy so later appends by the caller do not alter the recorded request.
	req.Messages = append([]llm.Message(nil), req.Messages...)
	s.Calls = append(s.Calls, req)
	if len(s.Calls) > len(s.Steps) {
		return llm.Response{}, fmt.Errorf("llmtest: unexpected call %d, only %d steps scripted", len(s.Calls), len(s.Steps))
	}
	st := s.Steps[len(s.Calls)-1]
	return st.Response, st.Err
}
