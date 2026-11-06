package llm_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/axonops/axai/internal/llm"
	"github.com/axonops/axai/internal/llm/llmtest"
)

func TestNoticeTokenLimit(t *testing.T) {
	for name, tc := range map[string]struct {
		resp llm.Response
		want bool
	}{
		"cut short": {llm.Response{StopReason: llm.StopMaxTokens, Usage: llm.Usage{OutputTokens: 316}}, true},
		"long":      {llm.Response{StopReason: llm.StopMaxTokens, Usage: llm.Usage{OutputTokens: 2000}}, false},
		"finished":  {llm.Response{StopReason: llm.StopEnd, Usage: llm.Usage{OutputTokens: 10}}, false},
	} {
		t.Run(name, func(t *testing.T) {
			var log bytes.Buffer
			p := llm.NoticeTokenLimit(&llmtest.Scripted{Steps: []llmtest.Step{{Response: tc.resp}}}, &log, "fixer")
			_, err := p.Complete(context.Background(), llm.Request{})
			require.NoError(t, err)
			if tc.want {
				require.Equal(t, "stage fixer: model stopped at its token limit; thinking may be using the budget (raise it or lower reasoning effort)\n", log.String())
			} else {
				require.Empty(t, log.String())
			}
		})
	}
}
