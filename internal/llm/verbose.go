package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

// Verbose wraps p so that each model response is written to w, for review.
// Requests are never written: they can carry host facts. stage names the
// pipeline stage that made the call.
func Verbose(p Provider, w io.Writer, stage string) Provider {
	return &verbose{Provider: p, w: w, stage: stage}
}

type verbose struct {
	Provider
	stage string

	mu sync.Mutex // serialises writes to w
	w  io.Writer
}

func (v *verbose) Complete(ctx context.Context, req Request) (Response, error) {
	start := time.Now()
	resp, err := v.Provider.Complete(ctx, req)
	d := time.Since(start).Round(time.Millisecond)

	var b bytes.Buffer
	if err != nil {
		fmt.Fprintf(&b, "--- model error: stage=%s provider=%s: %v ---\n", v.stage, v.Name(), err)
	} else {
		fmt.Fprintf(&b, "--- model response: stage=%s provider=%s duration=%s tokens_in=%d tokens_out=%d stop=%s ---\n",
			v.stage, v.Name(), d, resp.Usage.InputTokens, resp.Usage.OutputTokens, resp.StopReason)
		text := resp.Text
		var pretty bytes.Buffer
		if json.Valid([]byte(text)) && json.Indent(&pretty, []byte(text), "", "  ") == nil {
			text = pretty.String()
		}
		b.WriteString(text)
		if len(text) == 0 || text[len(text)-1] != '\n' {
			b.WriteByte('\n')
		}
		b.WriteString("--- end ---\n")
	}
	v.mu.Lock()
	_, _ = v.w.Write(b.Bytes())
	v.mu.Unlock()
	return resp, err
}
