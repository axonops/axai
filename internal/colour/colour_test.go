package colour

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEnabled(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	var buf bytes.Buffer
	assert.False(t, Enabled(&buf, false, env(nil)), "a buffer is not a terminal")
	assert.False(t, Enabled(&buf, true, env(nil)))
	assert.False(t, Enabled(&buf, false, env(map[string]string{"NO_COLOR": "1"})))
	assert.False(t, Enabled(&buf, false, env(map[string]string{"TERM": "dumb"})))
}

func TestPalette(t *testing.T) {
	assert.Equal(t, "OK", Palette{}.Status("OK"))
	assert.Equal(t, "\x1b[32mOK\x1b[0m", Palette{On: true}.Status("OK"))
	assert.Equal(t, "\x1b[31mFAIL\x1b[0m", Palette{On: true}.Status("FAIL"))
	assert.Equal(t, "\x1b[33mSKIP\x1b[0m", Palette{On: true}.Status("SKIP"))
	assert.Equal(t, "other", Palette{On: true}.Status("other"))
}

func TestLines(t *testing.T) {
	var buf bytes.Buffer
	w := Lines(&buf)
	n, err := w.Write([]byte("plain\nwarning: x\n--- model response\nwar"))
	assert.NoError(t, err)
	assert.Equal(t, 39, n)
	_, _ = w.Write([]byte("ning: not a prefix here\n"))
	assert.Equal(t, "plain\n\x1b[33mwarning: x\x1b[0m\n\x1b[2m--- model response\x1b[0m\nwarning: not a prefix here\n", buf.String())
}

func TestStrip(t *testing.T) {
	assert.Equal(t, "ok: [h]", Strip("\x1b[0;32mok: [h]\x1b[0m"))
	var buf bytes.Buffer
	n, err := StripWriter(&buf).Write([]byte("\x1b[1;31mfailed\x1b[0m\n"))
	assert.NoError(t, err)
	assert.Equal(t, 18, n)
	assert.Equal(t, "failed\n", buf.String())
}
