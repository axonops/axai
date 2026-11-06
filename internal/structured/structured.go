// Package structured gets JSON-schema-valid objects out of any llm.Provider,
// feeding validation errors back to the model for a bounded number of repairs.
package structured

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/axonops/axai/internal/llm"
)

const schemaInstruction = "Respond with a single JSON object and nothing else. It must conform to this JSON schema:\n"

// ErrRefused means the model refused the request.
var ErrRefused = errors.New("model refused the request")

// Request is one structured-output call.
type Request struct {
	Stage      string // used in errors, e.g. "architect"
	System     string
	Prompt     string
	Schema     map[string]any
	SchemaName string
	MaxRepairs int // default 2
	MaxTokens  int // output budget per call; 0 means the provider default
	// Check, when set, runs on every reply that passes the schema and gets
	// the decoded value (a T, not a pointer). Its error goes back to the
	// model like a schema error and, after the last repair, is wrapped in
	// the StageError.
	Check func(any) error
}

// StageError means no valid output was produced.
type StageError struct {
	Stage    string
	Attempts int
	Err      error
}

func (e *StageError) Error() string {
	return fmt.Sprintf("stage %s: no valid output after %d attempt(s): %v", e.Stage, e.Attempts, e.Err)
}

func (e *StageError) Unwrap() error { return e.Err }

// Generate asks p for a T that satisfies the request schema.
func Generate[T any](ctx context.Context, p llm.Provider, req Request) (T, error) {
	var zero T
	schemaJSON, err := json.Marshal(req.Schema)
	if err != nil {
		return zero, fmt.Errorf("stage %s: marshal schema: %w", req.Stage, err)
	}
	sch, err := compile(schemaJSON)
	if err != nil {
		return zero, fmt.Errorf("stage %s: compile schema: %w", req.Stage, err)
	}
	maxRepairs := req.MaxRepairs
	if maxRepairs <= 0 {
		maxRepairs = 2
	}

	// The schema always goes in the prompt too: servers without native schema
	// support rely on it, and it helps small local models that have it.
	system := strings.TrimSpace(req.System + "\n\n" + schemaInstruction + string(schemaJSON))
	msgs := []llm.Message{{Role: llm.RoleUser, Content: req.Prompt}}

	var lastErr error
	attempts := 0
	for attempts <= maxRepairs {
		attempts++
		resp, err := p.Complete(ctx, llm.Request{System: system, Messages: msgs, Schema: req.Schema, SchemaName: req.SchemaName, MaxTokens: req.MaxTokens})
		if err != nil {
			return zero, fmt.Errorf("stage %s: %w", req.Stage, err)
		}
		if resp.StopReason == llm.StopRefusal {
			return zero, &StageError{Stage: req.Stage, Attempts: attempts, Err: ErrRefused}
		}
		var out T
		if lastErr = decode(sch, resp.Text, &out); lastErr == nil {
			if req.Check == nil {
				return out, nil
			}
			if lastErr = req.Check(out); lastErr == nil {
				return out, nil
			}
		} else if resp.StopReason == llm.StopMaxTokens {
			lastErr = fmt.Errorf("output truncated at max tokens: %w", lastErr)
		}
		reply := resp.Text
		if strings.TrimSpace(reply) == "" {
			reply = "(empty reply)"
		}
		msgs = append(msgs,
			llm.Message{Role: llm.RoleAssistant, Content: reply},
			llm.Message{Role: llm.RoleUser, Content: "Your previous reply was not valid: " + lastErr.Error() + "\nReply again with only the corrected JSON object."},
		)
	}
	return zero, &StageError{Stage: req.Stage, Attempts: attempts, Err: lastErr}
}

func compile(schemaJSON []byte) (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaJSON))
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("schema.json", doc); err != nil {
		return nil, err
	}
	return c.Compile("schema.json")
}

func decode(sch *jsonschema.Schema, text string, out any) error {
	raw, err := ExtractJSON(text)
	if err != nil {
		return err
	}
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(raw))
	if err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	if err := sch.Validate(inst); err != nil {
		return fmt.Errorf("schema validation failed: %w", err)
	}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	return nil
}
