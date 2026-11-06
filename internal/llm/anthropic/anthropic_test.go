package anthropic_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/axonops/axai/internal/llm"
	"github.com/axonops/axai/internal/llm/anthropic"
)

// message renders a streamed reply as server-sent events.
func message(stop, text string) string {
	events := []string{
		`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5",` +
			`"content":[],"stop_reason":null,"usage":{"input_tokens":10,"output_tokens":0}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":` + quote(text) + `}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"` + stop + `"},"usage":{"output_tokens":5}}`,
		`{"type":"message_stop"}`,
	}
	var sb strings.Builder
	for _, e := range events {
		var ev struct{ Type string }
		_ = json.Unmarshal([]byte(e), &ev)
		sb.WriteString("event: " + ev.Type + "\ndata: " + e + "\n\n")
	}
	return sb.String()
}

func quote(s string) string { b, _ := json.Marshal(s); return string(b) }

func server(t *testing.T, status int, body string, got *map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// assert, not require: this runs on the server goroutine.
		assert.Equal(t, "/v1/messages", r.URL.Path)
		assert.Equal(t, "test-key", r.Header.Get("X-Api-Key"))
		b, _ := io.ReadAll(r.Body)
		if got != nil {
			_ = json.Unmarshal(b, got)
		}
		if status == http.StatusOK {
			w.Header().Set("Content-Type", "text/event-stream")
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func provider(t *testing.T, url string) *anthropic.Provider {
	t.Helper()
	p, err := anthropic.New(anthropic.Config{APIKey: "test-key", BaseURL: url})
	require.NoError(t, err)
	return p
}

func TestCompleteWithSchema(t *testing.T) {
	var body map[string]any
	srv := server(t, 200, message("end_turn", `{"a":1}`), &body)

	resp, err := provider(t, srv.URL).Complete(context.Background(), llm.Request{
		System: "be terse",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "q1"},
			{Role: llm.RoleAssistant, Content: "a1"},
			{Role: llm.RoleUser, Content: "q2"},
		},
		Schema: map[string]any{"type": "object", "additionalProperties": false},
	})
	require.NoError(t, err)
	require.Equal(t, `{"a":1}`, resp.Text)
	require.Equal(t, llm.StopEnd, resp.StopReason)
	require.Equal(t, llm.Usage{InputTokens: 10, OutputTokens: 5}, resp.Usage)

	require.Equal(t, "claude-opus-5-5", body["model"])
	require.EqualValues(t, 16000, body["max_tokens"])
	require.Equal(t, "be terse", body["system"].([]any)[0].(map[string]any)["text"])
	require.Len(t, body["messages"], 3)
	format := body["output_config"].(map[string]any)["format"].(map[string]any)
	require.Equal(t, "json_schema", format["type"])
	require.Equal(t, map[string]any{"type": "object", "additionalProperties": false}, format["schema"])
	require.NotContains(t, body, "temperature")
	require.NotContains(t, body, "thinking")
	require.Equal(t, true, body["stream"])
}

func TestLargeMaxTokensDoesNotRequireCallerStreaming(t *testing.T) {
	srv := server(t, 200, message("end_turn", "OK"), nil)
	resp, err := provider(t, srv.URL).Complete(context.Background(), llm.Request{
		MaxTokens: 64000,
		Messages:  []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	})
	require.NoError(t, err)
	require.Equal(t, "OK", resp.Text)
}

func TestNoSchemaOmitsOutputConfig(t *testing.T) {
	var body map[string]any
	srv := server(t, 200, message("end_turn", "OK"), &body)
	_, err := provider(t, srv.URL).Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	})
	require.NoError(t, err)
	require.NotContains(t, body, "output_config")
}

func TestStopReasons(t *testing.T) {
	for stop, want := range map[string]llm.StopReason{"max_tokens": llm.StopMaxTokens, "refusal": llm.StopRefusal} {
		srv := server(t, 200, message(stop, "x"), nil)
		resp, err := provider(t, srv.URL).Complete(context.Background(), llm.Request{
			Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
		})
		require.NoError(t, err)
		require.Equal(t, want, resp.StopReason, stop)
	}
}

func TestRateLimitIsTransient(t *testing.T) {
	srv := server(t, 429, `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`, nil)
	_, err := provider(t, srv.URL).Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	})
	var se *llm.StatusError
	require.ErrorAs(t, err, &se)
	require.Equal(t, 429, se.Code)
	require.True(t, llm.IsTransient(err))
}

func TestRejectsSystemRoleMessage(t *testing.T) {
	_, err := provider(t, "http://127.0.0.1:1").Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.RoleSystem, Content: "x"}},
	})
	require.ErrorContains(t, err, `unsupported message role "system"`)
}

func TestNewRequiresKey(t *testing.T) {
	_, err := anthropic.New(anthropic.Config{})
	require.ErrorContains(t, err, "ANTHROPIC_API_KEY")
}
