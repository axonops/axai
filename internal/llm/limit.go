package llm

import (
	"context"
	"fmt"
	"io"
	"sync"
)

// TokenLimitNotice is the line written when a reply stops at its token
// limit after little visible output.
const TokenLimitNotice = "model stopped at its token limit; thinking may be using the budget (raise it or lower reasoning effort)"

// thinkingSuspect is the output size under which a reply that hit the token
// limit most likely spent the budget on thinking.
const thinkingSuspect = 2000

// NoticeTokenLimit writes TokenLimitNotice to w when a reply from p stops at
// max_tokens with fewer than 2000 output tokens. Thinking models count their
// reasoning against max_tokens, so the reply is cut short and repaired at the
// cost of another call.
func NoticeTokenLimit(p Provider, w io.Writer, stage string) Provider {
	return &limitNotice{Provider: p, w: w, stage: stage}
}

type limitNotice struct {
	Provider
	stage string
	mu    sync.Mutex
	w     io.Writer
}

func (l *limitNotice) Complete(ctx context.Context, req Request) (Response, error) {
	resp, err := l.Provider.Complete(ctx, req)
	if err == nil && resp.StopReason == StopMaxTokens && resp.Usage.OutputTokens < thinkingSuspect {
		l.mu.Lock()
		fmt.Fprintf(l.w, "stage %s: %s\n", l.stage, TokenLimitNotice)
		l.mu.Unlock()
	}
	return resp, err
}
