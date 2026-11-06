package structured_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/axonops/axai/internal/structured"
)

func TestExtractJSON(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"plain":             {`{"a":1}`, `{"a":1}`},
		"fenced":            {"```json\n{\"a\":1}\n```", `{"a":1}`},
		"prose around":      {"Here you go:\n{\"a\":{\"b\":2}}\nThanks!", `{"a":{"b":2}}`},
		"brace in string":   {`{"a":"}{"}`, `{"a":"}{"}`},
		"escaped quote":     {`{"a":"x\"}"}`, `{"a":"x\"}"}`},
		"stray brace first": {"use {braces} like this {\"a\":1}", `{"a":1}`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := structured.ExtractJSON(tc.in)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestExtractJSONNone(t *testing.T) {
	for _, in := range []string{"", "no json here", `{"a":`, "[1,2]"} {
		_, err := structured.ExtractJSON(in)
		require.ErrorIs(t, err, structured.ErrNoJSON, in)
	}
}
