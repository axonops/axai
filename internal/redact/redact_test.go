package redact_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/axonops/axai/internal/llm"
	"github.com/axonops/axai/internal/llm/llmtest"
	"github.com/axonops/axai/internal/redact"
)

const secret = "s3cr3tValue_XYZ-123"

func TestString(t *testing.T) {
	var r redact.Redactor
	require.Equal(t, "pw "+secret, r.String("pw "+secret), "nothing to redact yet")
	r.Add(secret, "short", "")
	require.Equal(t, "pw ******** and ********.", r.String("pw "+secret+" and "+secret+"."))
	require.Equal(t, "short", r.String("short"), "values under 8 characters are kept")
	var nilR *redact.Redactor
	require.Equal(t, "x", nilR.String("x"))
}

func TestLongerValueWins(t *testing.T) {
	var r redact.Redactor
	r.Add("abcdefgh", "abcdefghijkl")
	require.Equal(t, "********", r.String("abcdefghijkl"))
}

func TestValue(t *testing.T) {
	var r redact.Redactor
	r.Add(secret)
	got := r.Map(map[string]any{"msg": "x " + secret, "list": []any{secret, 1}, secret: true, "n": 2})
	require.Equal(t, map[string]any{"msg": "x ********", "list": []any{"********", 1}, "********": true, "n": 2}, got)
}

// A value split across writes is still masked; text that cannot start a
// secret is written at once.
func TestWriter(t *testing.T) {
	var r redact.Redactor
	r.Add(secret)
	var out bytes.Buffer
	w := r.Writer(&out)
	for _, chunk := range []string{"ok: [h1] => " + secret[:5], secret[5:12], secret[12:] + "\n", "Apply this plan? [y/N] "} {
		_, err := w.Write([]byte(chunk))
		require.NoError(t, err)
	}
	require.Equal(t, "ok: [h1] => ********\nApply this plan? [y/N] ", out.String())
	_, err := w.Write([]byte("tail " + secret[:3]))
	require.NoError(t, err)
	require.NotContains(t, out.String(), "tail "+secret[:3])
	require.NoError(t, w.Flush())
	require.Contains(t, out.String(), "tail "+secret[:3])
}

// Values added after the writer was made are masked from then on.
func TestWriterLateValues(t *testing.T) {
	var r redact.Redactor
	var out bytes.Buffer
	w := r.Writer(&out)
	_, _ = w.Write([]byte("before\n"))
	r.Add(secret)
	_, _ = w.Write([]byte(secret + "\n"))
	require.Equal(t, "before\n********\n", out.String())
}

func TestProvider(t *testing.T) {
	var r redact.Redactor
	r.Add(secret)
	m := &llmtest.Scripted{Steps: []llmtest.Step{llmtest.Text("echo " + secret)}}
	resp, err := r.Provider(m).Complete(context.Background(), llm.Request{System: "s " + secret, Messages: []llm.Message{{Role: llm.RoleUser, Content: "failed: " + secret}}})
	require.NoError(t, err)
	require.Equal(t, "echo ********", resp.Text)
	require.Equal(t, "s ********", m.Calls[0].System)
	require.Equal(t, "failed: ********", m.Calls[0].Messages[0].Content)
}
