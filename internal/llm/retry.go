package llm

import (
	"context"
	"fmt"
	"time"
)

type retrying struct {
	Provider
	attempts int
	base     time.Duration
}

// WithRetry retries transient failures with exponential backoff starting at base.
func WithRetry(p Provider, attempts int, base time.Duration) Provider {
	if attempts < 1 {
		attempts = 1
	}
	return &retrying{Provider: p, attempts: attempts, base: base}
}

func (r *retrying) Complete(ctx context.Context, req Request) (Response, error) {
	var err error
	for i := 0; i < r.attempts; i++ {
		if i > 0 {
			t := time.NewTimer(r.base << (i - 1))
			select {
			case <-ctx.Done():
				t.Stop()
				return Response{}, ctx.Err()
			case <-t.C:
			}
		}
		var resp Response
		resp, err = r.Provider.Complete(ctx, req)
		if err == nil || !IsTransient(err) {
			return resp, err
		}
	}
	return Response{}, fmt.Errorf("after %d attempts: %w", r.attempts, err)
}
