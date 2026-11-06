// Package exectest provides a fake execx.Runner.
package exectest

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/axonops/axai/internal/execx"
)

// Handler answers a fake command.
type Handler func(c execx.Cmd) (execx.Result, error)

// Fake answers LookPath from Paths and Run from Handlers keyed by the
// program's base name. Unknown programs are errors.
type Fake struct {
	Paths    map[string]string
	Handlers map[string]Handler
	Calls    []execx.Cmd

	mu sync.Mutex
}

// LookPath resolves name from Paths.
func (f *Fake) LookPath(name string) (string, error) {
	if p, ok := f.Paths[name]; ok {
		return p, nil
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}

// Run records the command and runs its handler.
func (f *Fake) Run(_ context.Context, c execx.Cmd) (execx.Result, error) {
	f.mu.Lock()
	f.Calls = append(f.Calls, c)
	h := f.Handlers[filepath.Base(c.Name)]
	f.mu.Unlock()
	if h == nil {
		return execx.Result{}, fmt.Errorf("exectest: no handler for %s", c.Name)
	}
	return h(c)
}

// CallsTo returns the recorded calls whose program base name is name.
func (f *Fake) CallsTo(name string) []execx.Cmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []execx.Cmd
	for _, c := range f.Calls {
		if filepath.Base(c.Name) == name {
			out = append(out, c)
		}
	}
	return out
}

// EnvValue returns the value of key in c.Env, or "".
func EnvValue(c execx.Cmd, key string) string {
	for _, kv := range c.Env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v
		}
	}
	return ""
}
