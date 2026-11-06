package llm_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/axonops/axai/internal/llm"
	"github.com/axonops/axai/internal/llm/llmtest"
)

func status(code int) llmtest.Step {
	return llmtest.Step{Err: &llm.StatusError{Provider: "p", Code: code, Body: "x"}}
}

func TestRetryRecoversFromTransientError(t *testing.T) {
	s := &llmtest.Scripted{Steps: []llmtest.Step{status(429), status(503), llmtest.Text("ok")}}
	resp, err := llm.WithRetry(s, 3, time.Millisecond).Complete(context.Background(), llm.Request{})
	require.NoError(t, err)
	require.Equal(t, "ok", resp.Text)
	require.Len(t, s.Calls, 3)
}

func TestRetryStopsOnPermanentError(t *testing.T) {
	s := &llmtest.Scripted{Steps: []llmtest.Step{status(400), llmtest.Text("never")}}
	_, err := llm.WithRetry(s, 3, time.Millisecond).Complete(context.Background(), llm.Request{})
	var se *llm.StatusError
	require.ErrorAs(t, err, &se)
	require.Equal(t, 400, se.Code)
	require.Len(t, s.Calls, 1)
}

func TestRetryGivesUpAfterAttempts(t *testing.T) {
	s := &llmtest.Scripted{Steps: []llmtest.Step{status(500), status(500), status(500)}}
	_, err := llm.WithRetry(s, 3, time.Millisecond).Complete(context.Background(), llm.Request{})
	require.ErrorContains(t, err, "after 3 attempts")
	require.True(t, llm.IsTransient(err))
	require.Len(t, s.Calls, 3)
}

func TestRetryHonoursCancellation(t *testing.T) {
	s := &llmtest.Scripted{Steps: []llmtest.Step{status(500), llmtest.Text("ok")}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := llm.WithRetry(s, 3, time.Hour).Complete(ctx, llm.Request{})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Len(t, s.Calls, 1)
}

func TestParseSchemaMode(t *testing.T) {
	m, err := llm.ParseSchemaMode("json_object")
	require.NoError(t, err)
	require.Equal(t, llm.SchemaModeJSONObject, m)
	_, err = llm.ParseSchemaMode("xml")
	require.ErrorContains(t, err, `unknown schema mode "xml"`)
}

func TestIsTransient(t *testing.T) {
	post := func(err error) error {
		return fmt.Errorf("gemini: %w", &url.Error{Op: "Post", URL: "https://x/v1/chat/completions", Err: err})
	}
	for _, c := range []struct {
		name string
		err  error
		want bool
	}{
		{"unexpected EOF", post(io.ErrUnexpectedEOF), true},
		{"unexpected EOF reading the body", fmt.Errorf("p: read response: %w", io.ErrUnexpectedEOF), true},
		{"EOF from the transport", post(io.EOF), true},
		{"connection reset", post(&net.OpError{Op: "read", Net: "tcp", Err: &os.SyscallError{Syscall: "read", Err: syscall.ECONNRESET}}), true},
		{"broken pipe", post(&net.OpError{Op: "write", Net: "tcp", Err: &os.SyscallError{Syscall: "write", Err: syscall.EPIPE}}), true},
		{"timeout", post(&net.OpError{Op: "dial", Net: "tcp", Err: os.ErrDeadlineExceeded}), true},
		{"rate limit", &llm.StatusError{Code: 429}, true},
		{"server error", &llm.StatusError{Code: 502}, true},
		{"bare EOF", fmt.Errorf("p: decode: %w", io.EOF), false},
		{"client error", &llm.StatusError{Code: 400}, false},
		{"connection refused", post(&net.OpError{Op: "dial", Net: "tcp", Err: &os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED}}), false},
		{"other", errors.New("boom"), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, llm.IsTransient(c.err))
		})
	}
}

func TestRetryRecoversFromDroppedConnection(t *testing.T) {
	drop := &url.Error{Op: "Post", URL: "https://x/v1/chat/completions", Err: io.ErrUnexpectedEOF}
	s := &llmtest.Scripted{Steps: []llmtest.Step{{Err: drop}, llmtest.Text("ok")}}
	resp, err := llm.WithRetry(s, 3, time.Millisecond).Complete(context.Background(), llm.Request{})
	require.NoError(t, err)
	require.Equal(t, "ok", resp.Text)
	require.Len(t, s.Calls, 2)
}
