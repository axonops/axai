package claudecli_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/axonops/axai/internal/llm"
	"github.com/axonops/axai/internal/llm/claudecli"
)

// fake writes a shell script that records its arguments and stdin, then
// prints out.
func fake(t *testing.T, out string, exit int) (bin, dir string) {
	t.Helper()
	dir = t.TempDir()
	bin = filepath.Join(dir, "claude")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"" + dir + "/args\"\ncat > \"" + dir + "/stdin\"\n" +
		"echo \"$CLAUDE_CODE_MAX_OUTPUT_TOKENS\" > \"" + dir + "/maxtok\"\ncat <<'EOF'\n" + out + "\nEOF\nexit " + strconv.Itoa(exit) + "\n"
	require.NoError(t, os.WriteFile(bin, []byte(script), 0o755))
	return bin, dir
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	require.NoError(t, err)
	return string(b)
}

func TestMissingBinary(t *testing.T) {
	_, err := claudecli.New(claudecli.Config{Binary: "definitely-not-a-real-claude"})
	require.ErrorContains(t, err, "not found on PATH")
}

func TestText(t *testing.T) {
	bin, dir := fake(t, `{"type":"result","is_error":false,"result":"hello","stop_reason":"end_turn","usage":{"input_tokens":2,"cache_read_input_tokens":8,"output_tokens":5}}`, 0)
	p, err := claudecli.New(claudecli.Config{Binary: bin, Model: "sonnet"})
	require.NoError(t, err)
	require.Equal(t, "claude-cli", p.Name())
	resp, err := p.Complete(context.Background(), llm.Request{
		System:    "be terse",
		Messages:  []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
		MaxTokens: 123,
	})
	require.NoError(t, err)
	require.Equal(t, "hello", resp.Text)
	require.Equal(t, llm.StopEnd, resp.StopReason)
	require.Equal(t, llm.Usage{InputTokens: 10, OutputTokens: 5}, resp.Usage)
	require.Equal(t, "hi", read(t, dir+"/stdin"))
	require.Equal(t, "123\n", read(t, dir+"/maxtok"))
	args := read(t, dir+"/args")
	require.Contains(t, args, "--model\nsonnet")
	require.Contains(t, args, "--system-prompt\nbe terse")
	require.NotContains(t, args, "--json-schema")
}

func TestSchemaUsesStructuredOutput(t *testing.T) {
	bin, dir := fake(t, `{"type":"result","is_error":false,"result":"ignored","structured_output":{"a":"b"},"usage":{}}`, 0)
	p, err := claudecli.New(claudecli.Config{Binary: bin})
	require.NoError(t, err)
	resp, err := p.Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}},
		Schema:   map[string]any{"type": "object"},
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"a":"b"}`, resp.Text)
	require.Contains(t, read(t, dir+"/args"), `--json-schema`)
}

func TestMultiTurnTranscript(t *testing.T) {
	bin, dir := fake(t, `{"type":"result","is_error":false,"result":"ok","usage":{}}`, 0)
	p, err := claudecli.New(claudecli.Config{Binary: bin})
	require.NoError(t, err)
	_, err = p.Complete(context.Background(), llm.Request{Messages: []llm.Message{
		{Role: llm.RoleUser, Content: "a"}, {Role: llm.RoleAssistant, Content: "b"}, {Role: llm.RoleUser, Content: "c"},
	}})
	require.NoError(t, err)
	require.Equal(t, "Human: a\n\nAssistant: b\n\nHuman: c\n\nAssistant:", read(t, dir+"/stdin"))
}

func TestStopReasons(t *testing.T) {
	for in, want := range map[string]llm.StopReason{"max_tokens": llm.StopMaxTokens, "refusal": llm.StopRefusal} {
		bin, _ := fake(t, `{"type":"result","is_error":false,"result":"x","stop_reason":"`+in+`","usage":{}}`, 0)
		p, err := claudecli.New(claudecli.Config{Binary: bin})
		require.NoError(t, err)
		resp, err := p.Complete(context.Background(), llm.Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}}})
		require.NoError(t, err)
		require.Equal(t, want, resp.StopReason)
	}
}

func TestErrors(t *testing.T) {
	complete := func(out string, exit int) error {
		bin, _ := fake(t, out, exit)
		p, err := claudecli.New(claudecli.Config{Binary: bin})
		require.NoError(t, err)
		_, err = p.Complete(context.Background(), llm.Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}}})
		return err
	}

	err := complete(`{"type":"result","is_error":true,"result":"Not logged in","usage":{}}`, 1)
	require.ErrorContains(t, err, "Not logged in")

	err = complete(`{"type":"result","is_error":true,"result":"overloaded","api_error_status":529,"usage":{}}`, 1)
	require.True(t, llm.IsTransient(err))

	require.ErrorContains(t, complete(`not json`, 0), "decode output")
	require.ErrorContains(t, complete(`boom`, 2), "exit status 2")

	bin, _ := fake(t, `{}`, 0)
	p, err := claudecli.New(claudecli.Config{Binary: bin})
	require.NoError(t, err)
	_, err = p.Complete(context.Background(), llm.Request{})
	require.ErrorContains(t, err, "no messages")
}
