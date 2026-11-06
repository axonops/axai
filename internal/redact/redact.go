// Package redact hides the secret values axai generated wherever text
// leaves the process: logs, the JSON summary, state files and model
// prompts. Secrets are matched by value, so a value that reaches output
// through any variable or template is still hidden.
package redact

import (
	"context"
	"io"
	"slices"
	"strings"
	"sync"

	"github.com/axonops/axai/internal/llm"
)

// Mask replaces each secret value.
const Mask = "********"

// MinLength is the shortest value that is redacted; shorter values would
// hide ordinary words.
const MinLength = 8

// Redactor holds the secret values of a run. The zero value redacts
// nothing; values can be added once they are known, and every writer and
// provider built from the Redactor uses them from then on.
type Redactor struct {
	mu     sync.RWMutex
	values []string // longest first, so a value inside another is masked by the longer one
}

// Add records secret values. Values shorter than MinLength are ignored.
func (r *Redactor) Add(values ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range values {
		if len(v) >= MinLength && !slices.Contains(r.values, v) {
			r.values = append(r.values, v)
		}
	}
	slices.SortFunc(r.values, func(a, b string) int { return len(b) - len(a) })
}

func (r *Redactor) snapshot() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.values
}

// String masks every secret value in s.
func (r *Redactor) String(s string) string {
	for _, v := range r.snapshot() {
		s = strings.ReplaceAll(s, v, Mask)
	}
	return s
}

// Value masks secret values in every string inside v, which holds decoded
// JSON or YAML (maps, slices and scalars). Map keys are masked too.
func (r *Redactor) Value(v any) any {
	if len(r.snapshot()) == 0 {
		return v
	}
	switch t := v.(type) {
	case string:
		return r.String(t)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			out[r.String(k)] = r.Value(x)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = r.Value(x)
		}
		return out
	}
	return v
}

// Map masks the values of a decoded JSON object.
func (r *Redactor) Map(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out, _ := r.Value(m).(map[string]any)
	return out
}

// Writer masks secret values in what is written to w. A write that ends
// with the start of a secret is held back until the next write shows
// whether the secret follows, so a value split across writes is still
// masked. Flush writes what is held back.
type Writer struct {
	r   *Redactor
	mu  sync.Mutex
	w   io.Writer
	buf []byte
}

// Writer returns a masking writer for w.
func (r *Redactor) Writer(w io.Writer) *Writer { return &Writer{r: r, w: w} }

func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	values := w.r.snapshot()
	if len(values) == 0 && len(w.buf) == 0 {
		return w.w.Write(p)
	}
	w.buf = append(w.buf, p...)
	text := w.r.String(string(w.buf))
	hold := heldBack(text, values)
	if _, err := io.WriteString(w.w, text[:len(text)-hold]); err != nil {
		return 0, err
	}
	w.buf = append(w.buf[:0], text[len(text)-hold:]...)
	return len(p), nil
}

// Flush writes what Write held back.
func (w *Writer) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) == 0 {
		return nil
	}
	_, err := io.WriteString(w.w, w.r.String(string(w.buf)))
	w.buf = w.buf[:0]
	return err
}

// heldBack is the length of the longest end of text that starts a secret.
func heldBack(text string, values []string) int {
	n := 0
	for _, v := range values {
		for k := min(len(v)-1, len(text)); k > n; k-- {
			if strings.HasSuffix(text, v[:k]) {
				n = k
				break
			}
		}
	}
	return n
}

// Provider masks secret values in everything sent to p and in its replies.
func (r *Redactor) Provider(p llm.Provider) llm.Provider { return &provider{Provider: p, r: r} }

type provider struct {
	llm.Provider
	r *Redactor
}

func (p *provider) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	req.System = p.r.String(req.System)
	msgs := make([]llm.Message, len(req.Messages))
	for i, m := range req.Messages {
		m.Content = p.r.String(m.Content)
		msgs[i] = m
	}
	req.Messages = msgs
	resp, err := p.Provider.Complete(ctx, req)
	resp.Text = p.r.String(resp.Text)
	return resp, err
}

// Bytes masks secret values in b.
func (r *Redactor) Bytes(b []byte) []byte {
	if len(r.snapshot()) == 0 {
		return b
	}
	return []byte(r.String(string(b)))
}
