package structured_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/axonops/axai/internal/llm"
	"github.com/axonops/axai/internal/llm/llmtest"
	"github.com/axonops/axai/internal/structured"
)

type answer struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

var schema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"name":  map[string]any{"type": "string"},
		"count": map[string]any{"type": "integer", "minimum": 1},
	},
	"required":             []any{"name", "count"},
	"additionalProperties": false,
}

func request() structured.Request {
	return structured.Request{Stage: "test", System: "You count things.", Prompt: "count", Schema: schema, SchemaName: "answer"}
}

func TestGenerateFirstTry(t *testing.T) {
	p := &llmtest.Scripted{Steps: []llmtest.Step{llmtest.Text("```json\n{\"name\":\"x\",\"count\":2}\n```")}}
	got, err := structured.Generate[answer](context.Background(), p, request())
	require.NoError(t, err)
	require.Equal(t, answer{Name: "x", Count: 2}, got)

	require.Len(t, p.Calls, 1)
	call := p.Calls[0]
	require.Contains(t, call.System, "You count things.")
	require.Contains(t, call.System, `"additionalProperties":false`)
	require.Equal(t, schema, call.Schema)
	require.Equal(t, "answer", call.SchemaName)
	require.Zero(t, call.MaxTokens, "0 leaves the output budget to the provider")
	require.Equal(t, []llm.Message{{Role: llm.RoleUser, Content: "count"}}, call.Messages)
}

func TestGenerateRepairsInvalidOutput(t *testing.T) {
	p := &llmtest.Scripted{Steps: []llmtest.Step{
		llmtest.Text(`{"name":"x","count":0}`),
		llmtest.Text(`{"name":"x","count":3}`),
	}}
	got, err := structured.Generate[answer](context.Background(), p, request())
	require.NoError(t, err)
	require.Equal(t, 3, got.Count)

	second := p.Calls[1].Messages
	require.Len(t, second, 3)
	require.Equal(t, llm.RoleAssistant, second[1].Role)
	require.Equal(t, `{"name":"x","count":0}`, second[1].Content)
	require.Contains(t, second[2].Content, "not valid")
}

func TestGenerateGivesUp(t *testing.T) {
	p := &llmtest.Scripted{Steps: []llmtest.Step{llmtest.Text("nope"), llmtest.Text(""), llmtest.Text(`{"name":1}`)}}
	_, err := structured.Generate[answer](context.Background(), p, request())
	var se *structured.StageError
	require.ErrorAs(t, err, &se)
	require.Equal(t, "test", se.Stage)
	require.Equal(t, 3, se.Attempts)
	require.ErrorContains(t, err, "stage test")
	// An empty reply is replaced so providers that reject empty turns accept the history.
	require.Equal(t, "(empty reply)", p.Calls[2].Messages[3].Content)
}

func TestGenerateRefusal(t *testing.T) {
	p := &llmtest.Scripted{Steps: []llmtest.Step{{Response: llm.Response{StopReason: llm.StopRefusal}}}}
	_, err := structured.Generate[answer](context.Background(), p, request())
	require.ErrorIs(t, err, structured.ErrRefused)
	require.Len(t, p.Calls, 1)
}

func TestGenerateProviderError(t *testing.T) {
	boom := errors.New("connection refused")
	p := &llmtest.Scripted{Steps: []llmtest.Step{{Err: boom}}}
	_, err := structured.Generate[answer](context.Background(), p, request())
	require.ErrorIs(t, err, boom)
	require.ErrorContains(t, err, "stage test")
}

func TestGenerateInvalidSchema(t *testing.T) {
	req := request()
	req.Schema = map[string]any{"type": 5}
	_, err := structured.Generate[answer](context.Background(), &llmtest.Scripted{}, req)
	require.ErrorContains(t, err, "compile schema")
}

func TestGenerateWithoutCheckAcceptsFirstValidReply(t *testing.T) {
	p := &llmtest.Scripted{Steps: []llmtest.Step{llmtest.Text(`{"name":"x","count":2}`)}}
	req := request()
	require.Nil(t, req.Check)
	got, err := structured.Generate[answer](context.Background(), p, req)
	require.NoError(t, err)
	require.Equal(t, answer{Name: "x", Count: 2}, got)
	require.Len(t, p.Calls, 1)
}

func TestGenerateCheckRepairs(t *testing.T) {
	p := &llmtest.Scripted{Steps: []llmtest.Step{
		llmtest.Text("nope"),
		llmtest.Text(`{"name":"bad","count":2}`),
		llmtest.Text(`{"name":"good","count":2}`),
	}}
	var seen []answer
	req := request()
	req.Check = func(v any) error {
		a, ok := v.(answer)
		require.True(t, ok)
		seen = append(seen, a)
		if a.Name != "good" {
			return errors.New("name must be good")
		}
		return nil
	}
	got, err := structured.Generate[answer](context.Background(), p, req)
	require.NoError(t, err)
	require.Equal(t, "good", got.Name)
	// Check runs only on replies that passed the schema.
	require.Equal(t, []answer{{Name: "bad", Count: 2}, {Name: "good", Count: 2}}, seen)
	require.Contains(t, p.Calls[2].Messages[4].Content, "not valid: name must be good")
}

func TestGenerateCheckGivesUp(t *testing.T) {
	bad := errors.New("still wrong")
	reply := llmtest.Text(`{"name":"x","count":2}`)
	p := &llmtest.Scripted{Steps: []llmtest.Step{reply, reply, reply}}
	req := request()
	req.Check = func(any) error { return fmt.Errorf("check: %w", bad) }
	_, err := structured.Generate[answer](context.Background(), p, req)
	var se *structured.StageError
	require.ErrorAs(t, err, &se)
	require.Equal(t, 3, se.Attempts)
	require.ErrorIs(t, err, bad)
}
