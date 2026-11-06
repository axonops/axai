// Package pipeline runs the deterministic stages after design: guard,
// compose, plan, confirm, apply with a bounded fixer loop, and state.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/axonops/axai/internal/compose"
	"github.com/axonops/axai/internal/fsutil"
	"github.com/axonops/axai/internal/guard"
	"github.com/axonops/axai/internal/lock"
	"github.com/axonops/axai/internal/runner"
	"github.com/axonops/axai/internal/spec"
	"github.com/axonops/axai/internal/state"
)

// Reviser returns a corrected spec after a failed run; it must return only
// specs that pass the guard.
type Reviser func(ctx context.Context, current spec.Spec, failure string) (spec.Spec, error)

// Options configures a run.
type Options struct {
	Dir      string
	Request  string
	Spec     spec.Spec
	Runner   runner.Runner
	Out      io.Writer
	Yes      bool
	Confirm  func(prompt string) (bool, error) // nil means decline
	Reviser  Reviser                           // nil disables the fixer
	MaxFixes int
	Commit   func(ctx context.Context, message string) // nil skips
	Now      func() time.Time
}

// Outcome describes how a run ended.
type Outcome string

// Outcomes.
const (
	NoChanges Outcome = "no changes"
	Planned   Outcome = "planned"
	Declined  Outcome = "declined"
	Applied   Outcome = "applied"
)

// ErrApplyFailed means apply failed and the fixer ran out of attempts.
var ErrApplyFailed = errors.New("apply failed")

// Plan writes the project and shows the plan.
func Plan(ctx context.Context, o Options) (Outcome, error) {
	if err := guard.Validate(o.Spec); err != nil {
		return "", err
	}
	l, err := lock.Acquire(o.Dir)
	if err != nil {
		return "", err
	}
	defer func() { _ = l.Release() }()
	return plan(ctx, &o)
}

func plan(ctx context.Context, o *Options) (Outcome, error) {
	if err := guard.Validate(o.Spec); err != nil {
		return "", err
	}
	if err := write(o.Dir, o.Spec); err != nil {
		return "", err
	}
	if err := state.SaveSpec(o.Dir, o.Spec); err != nil {
		return "", err
	}
	if err := o.Runner.Init(ctx); err != nil {
		return "", err
	}
	res, err := o.Runner.Plan(ctx)
	if err != nil {
		return "", err
	}
	if o.Out != nil {
		fmt.Fprintln(o.Out, res.Output)
	}
	if !res.Changes {
		return NoChanges, save(o, state.StatusApplied, nil)
	}
	return Planned, save(o, state.StatusPlanned, nil)
}

// Apply plans, asks for confirmation and applies, asking the fixer to repair
// failures up to MaxFixes times.
func Apply(ctx context.Context, o Options) (Outcome, error) {
	if err := guard.Validate(o.Spec); err != nil {
		return "", err
	}
	l, err := lock.Acquire(o.Dir)
	if err != nil {
		return "", err
	}
	defer func() { _ = l.Release() }()

	out, err := plan(ctx, &o)
	if err != nil || out == NoChanges {
		return out, err
	}
	if o.Spec.Destructive() || !o.Yes {
		prompt := "Apply this plan?"
		if o.Spec.Destructive() {
			prompt = "This plan DELETES resources. Apply anyway?"
		}
		ok := false
		if o.Confirm != nil {
			if ok, err = o.Confirm(prompt); err != nil {
				return "", err
			}
		}
		if !ok {
			return Declined, nil
		}
	}

	var fixes []state.Fix
	for attempt := 0; ; attempt++ {
		res, aerr := o.Runner.Apply(ctx)
		if aerr == nil {
			if o.Out != nil {
				fmt.Fprintln(o.Out, res.Output)
			}
			if err := save(&o, state.StatusApplied, fixes); err != nil {
				return "", err
			}
			if o.Commit != nil {
				o.Commit(context.WithoutCancel(ctx), commitMessage(o.Spec))
			}
			return Applied, nil
		}
		var f *runner.Failure
		if !errors.As(aerr, &f) {
			return "", aerr
		}
		if o.Reviser == nil || attempt >= o.MaxFixes {
			_ = save(&o, state.StatusFailed, fixes)
			return "", fmt.Errorf("%w after %d fix attempt(s): %s", ErrApplyFailed, attempt, f.Output)
		}
		next, rerr := o.Reviser(ctx, o.Spec, f.Output)
		if rerr != nil {
			_ = save(&o, state.StatusFailed, fixes)
			return "", fmt.Errorf("%w: fixer: %v", ErrApplyFailed, rerr)
		}
		fixes = append(fixes, state.Fix{Attempt: attempt + 1, Error: truncate(f.Output, 2000), Files: changedFiles(o.Spec, next)})
		o.Spec = next
		if o.Spec.Destructive() {
			ok := false
			if o.Confirm != nil {
				if ok, err = o.Confirm("The fixer's change DELETES resources. Apply anyway?"); err != nil {
					return "", err
				}
			}
			if !ok {
				return Declined, save(&o, state.StatusPlanned, fixes)
			}
		}
		if _, err := plan(ctx, &o); err != nil {
			return "", err
		}
	}
}

func write(dir string, s spec.Spec) error {
	files, err := compose.Render(s)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			return err
		}
		if err := fsutil.WriteFile(p, []byte(content), 0o640); err != nil {
			return err
		}
	}
	return nil
}

func changedFiles(a, b spec.Spec) []string {
	fa, _ := compose.Render(a)
	fb, _ := compose.Render(b)
	var out []string
	for n, c := range fb {
		if fa[n] != c {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

func save(o *Options, status string, fixes []state.Fix) error {
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	return state.Save(o.Dir, state.State{
		Status: status, Backend: string(o.Spec.Backend), Request: o.Request,
		Resources: len(o.Spec.Resources), Updated: now().UTC(), Fixes: fixes,
	})
}

func commitMessage(s spec.Spec) string {
	return fmt.Sprintf("axai: apply %s %s for %s (%d resources)", s.ClusterType, s.Backend, s.Cluster, len(s.Resources))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
