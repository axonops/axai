package openaicompat_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/axonops/axai/internal/llm"
	"github.com/axonops/axai/internal/llm/openaicompat"
)

const okBody = `{"choices":[{"message":{"content":"{\"a\":1}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":7}}`

type captured struct {
	body map[string]any
	auth string
	path string
}

func server(t *testing.T, status int, body string) (*httptest.Server, *captured) {
	t.Helper()
	c := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		c.body = nil // Unmarshal into an existing map keeps stale keys
		_ = json.Unmarshal(b, &c.body)
		c.auth = r.Header.Get("Authorization")
		c.path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, c
}

func newProvider(t *testing.T, url string, mode llm.SchemaMode) *openaicompat.Provider {
	t.Helper()
	p, err := openaicompat.New(openaicompat.Config{BaseURL: url + "/v1/", APIKey: "k", Model: "m", SchemaMode: mode})
	require.NoError(t, err)
	return p
}

var schemaReq = llm.Request{
	System:     "be terse",
	Messages:   []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	Schema:     map[string]any{"type": "object"},
	SchemaName: "answer",
}

func TestNativeSchemaMode(t *testing.T) {
	srv, c := server(t, 200, okBody)
	resp, err := newProvider(t, srv.URL, llm.SchemaModeNative).Complete(context.Background(), schemaReq)
	require.NoError(t, err)

	require.Equal(t, "/v1/chat/completions", c.path)
	require.Equal(t, "Bearer k", c.auth)
	require.Equal(t, "m", c.body["model"])
	require.EqualValues(t, 8192, c.body["max_tokens"])
	msgs := c.body["messages"].([]any)
	require.Equal(t, map[string]any{"role": "system", "content": "be terse"}, msgs[0])
	require.Equal(t, map[string]any{"role": "user", "content": "hi"}, msgs[1])
	rf := c.body["response_format"].(map[string]any)
	require.Equal(t, "json_schema", rf["type"])
	js := rf["json_schema"].(map[string]any)
	require.Equal(t, "answer", js["name"])
	require.Equal(t, map[string]any{"type": "object"}, js["schema"])

	require.Equal(t, `{"a":1}`, resp.Text)
	require.Equal(t, llm.StopEnd, resp.StopReason)
	require.Equal(t, llm.Usage{InputTokens: 11, OutputTokens: 7}, resp.Usage)
}

func TestJSONObjectAndPromptModes(t *testing.T) {
	srv, c := server(t, 200, okBody)
	_, err := newProvider(t, srv.URL, llm.SchemaModeJSONObject).Complete(context.Background(), schemaReq)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"type": "json_object"}, c.body["response_format"])

	_, err = newProvider(t, srv.URL, llm.SchemaModePrompt).Complete(context.Background(), schemaReq)
	require.NoError(t, err)
	require.NotContains(t, c.body, "response_format")
}

func TestMaxTokensFieldAndNoKey(t *testing.T) {
	srv, c := server(t, 200, okBody)
	p, err := openaicompat.New(openaicompat.Config{BaseURL: srv.URL, Model: "m", MaxTokensField: "max_completion_tokens", MaxTokens: 100})
	require.NoError(t, err)
	_, err = p.Complete(context.Background(), llm.Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}}})
	require.NoError(t, err)
	require.EqualValues(t, 100, c.body["max_completion_tokens"])
	require.NotContains(t, c.body, "max_tokens")
	require.Empty(t, c.auth)
}

func TestFinishReasons(t *testing.T) {
	for reason, want := range map[string]llm.StopReason{"length": llm.StopMaxTokens, "content_filter": llm.StopRefusal} {
		srv, _ := server(t, 200, `{"choices":[{"message":{"content":"x"},"finish_reason":"`+reason+`"}]}`)
		resp, err := newProvider(t, srv.URL, llm.SchemaModePrompt).Complete(context.Background(), schemaReq)
		require.NoError(t, err)
		require.Equal(t, want, resp.StopReason, reason)
	}
}

func TestStatusErrors(t *testing.T) {
	srv, _ := server(t, 429, `{"error":{"message":"slow down"}}`)
	_, err := newProvider(t, srv.URL, llm.SchemaModePrompt).Complete(context.Background(), schemaReq)
	var se *llm.StatusError
	require.ErrorAs(t, err, &se)
	require.Equal(t, 429, se.Code)
	require.Contains(t, se.Body, "slow down")
	require.True(t, llm.IsTransient(err))

	srv, _ = server(t, 401, `{"error":"bad key"}`)
	_, err = newProvider(t, srv.URL, llm.SchemaModePrompt).Complete(context.Background(), schemaReq)
	require.False(t, llm.IsTransient(err))
}

func TestEmptyChoices(t *testing.T) {
	srv, _ := server(t, 200, `{"choices":[]}`)
	_, err := newProvider(t, srv.URL, llm.SchemaModePrompt).Complete(context.Background(), schemaReq)
	require.ErrorContains(t, err, "response has no choices")
}

func TestNewValidates(t *testing.T) {
	_, err := openaicompat.New(openaicompat.Config{Model: "m"})
	require.ErrorContains(t, err, "base URL is required")
	_, err = openaicompat.New(openaicompat.Config{BaseURL: "http://x"})
	require.ErrorContains(t, err, "model is required")
}

func TestRefusal(t *testing.T) {
	srv, _ := server(t, 200, `{"choices":[{"message":{"content":"","refusal":"I can't help with that"},"finish_reason":"stop"}]}`)
	resp, err := newProvider(t, srv.URL, llm.SchemaModePrompt).Complete(context.Background(), schemaReq)
	require.NoError(t, err)
	require.Equal(t, llm.StopRefusal, resp.StopReason)
	require.Equal(t, "I can't help with that", resp.Text)
}
