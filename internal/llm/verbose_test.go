package llm_test

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/axonops/axai/internal/llm"
	"github.com/axonops/axai/internal/llm/llmtest"
)

func verboseRun(t *testing.T, step llmtest.Step) (string, llm.Provider) {
	t.Helper()
	var buf bytes.Buffer
	inner := &llmtest.Scripted{ProviderName: "fake", Steps: []llmtest.Step{step}}
	p := llm.Verbose(inner, &buf, "architect")
	_, _ = p.Complete(context.Background(), llm.Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: "secret-fact"}}})
	return buf.String(), p
}

func TestVerbose_JSONResponse_PrettyPrinted(t *testing.T) {
	step := llmtest.Step{Response: llm.Response{Text: `{"a":1,"b":[2]}`, StopReason: llm.StopEnd, Usage: llm.Usage{InputTokens: 7, OutputTokens: 3}}}
	got, _ := verboseRun(t, step)
	require.Regexp(t, `^--- model response: stage=architect provider=fake duration=\S+ tokens_in=7 tokens_out=3 stop=end ---\n`, got)
	require.Contains(t, got, "{\n  \"a\": 1,\n  \"b\": [\n    2\n  ]\n}\n--- end ---\n")
	require.NotContains(t, got, "secret-fact")
}

func TestVerbose_PlainResponse_Raw(t *testing.T) {
	got, _ := verboseRun(t, llmtest.Text("not { json"))
	require.Contains(t, got, "\nnot { json\n--- end ---\n")
}

func TestVerbose_Error_OneLine(t *testing.T) {
	got, _ := verboseRun(t, llmtest.Step{Err: errors.New("boom")})
	require.Equal(t, "--- model error: stage=architect provider=fake: boom ---\n", got)
}

func TestVerbose_PassesNameAndCapabilities(t *testing.T) {
	_, p := verboseRun(t, llmtest.Text("x"))
	require.Equal(t, "fake", p.Name())
	require.Equal(t, llm.SchemaModeNative, p.Capabilities().SchemaMode)
}

func TestVerbose_Concurrent(t *testing.T) {
	var buf bytes.Buffer
	steps := make([]llmtest.Step, 20)
	for i := range steps {
		steps[i] = llmtest.Text("hello")
	}
	p := llm.Verbose(&llmtest.Scripted{Steps: steps}, &buf, "s")
	var wg sync.WaitGroup
	for range steps {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = p.Complete(context.Background(), llm.Request{})
		}()
	}
	wg.Wait()
	require.Equal(t, 20, bytes.Count(buf.Bytes(), []byte("--- end ---\n")))
}
