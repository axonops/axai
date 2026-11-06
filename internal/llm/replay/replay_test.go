package replay_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/axonops/axai/internal/llm"
	"github.com/axonops/axai/internal/llm/llmtest"
	"github.com/axonops/axai/internal/llm/replay"
)

func req(prompt string) llm.Request {
	return llm.Request{
		System:   "sys",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: prompt}},
		Schema:   map[string]any{"type": "object", "required": []any{"a"}},
	}
}

func TestRecordThenReplay(t *testing.T) {
	dir := t.TempDir()
	inner := &llmtest.Scripted{Steps: []llmtest.Step{llmtest.Text(`{"a":1}`)}}

	rec := replay.NewRecorder(inner, dir)
	got, err := rec.Complete(context.Background(), req("hello"))
	require.NoError(t, err)
	require.Equal(t, `{"a":1}`, got.Text)

	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	require.Len(t, files, 1)

	again, err := replay.New(dir).Complete(context.Background(), req("hello"))
	require.NoError(t, err)
	require.Equal(t, got, again)
}

func TestReplayMissingFixture(t *testing.T) {
	_, err := replay.New(t.TempDir()).Complete(context.Background(), req("unseen"))
	require.ErrorContains(t, err, "replay: no fixture")
	require.ErrorContains(t, err, `"sys"`)
}

func TestKeyIgnoresMapInsertionOrder(t *testing.T) {
	a := llm.Request{Schema: map[string]any{"x": 1, "y": 2}}
	b := llm.Request{Schema: map[string]any{"y": 2, "x": 1}}
	ka, err := replay.Key(a)
	require.NoError(t, err)
	kb, _ := replay.Key(b)
	require.Equal(t, ka, kb)
	require.Len(t, ka, 16)
}

func TestReplayCorruptFixture(t *testing.T) {
	dir := t.TempDir()
	key, _ := replay.Key(req("x"))
	require.NoError(t, os.WriteFile(filepath.Join(dir, key+".json"), []byte("{not json"), 0o600))
	_, err := replay.New(dir).Complete(context.Background(), req("x"))
	require.ErrorContains(t, err, "replay: decode")
}
