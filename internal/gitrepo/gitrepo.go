// Package gitrepo records each plan and apply as a commit in the project's
// own git repository (spec 3.9).
package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/axonops/axai/internal/execx"
)

// ErrNoGit is returned by Commit when git is not installed. Commit prints
// nothing for it; the caller reports it once.
var ErrNoGit = errors.New("git not found; the project is not committed")

// Repo is a project git repository.
type Repo struct {
	Exec execx.Runner
	Dir  string
	Log  io.Writer // notices about commits that were skipped
}

// Commit commits every change in the project. It does nothing, with a notice,
// when git is missing or when the project sits inside another repository,
// such as a CI checkout, whose history belongs to the user. It never prompts
// and never signs, so it cannot hang a pipeline.
func (r Repo) Commit(ctx context.Context, message string) error {
	bin, err := r.Exec.LookPath("git")
	if err != nil {
		return ErrNoGit
	}
	git := func(args ...string) (execx.Result, error) {
		return r.Exec.Run(ctx, execx.Cmd{
			Name: bin, Args: append([]string{"-C", r.Dir}, args...),
			Env: []string{"GIT_TERMINAL_PROMPT=0"}, Unset: execx.SecretEnv,
		})
	}
	res, err := git("rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	if res.ExitCode == 0 {
		if top := strings.TrimSpace(string(res.Stdout)); !sameDir(top, r.Dir) {
			fmt.Fprintf(r.Log, "the project is inside the git repository %s; not committing\n", top)
			return nil
		}
	} else if err := check(git("init", "-q")); err != nil {
		return err
	}
	if err := check(git("add", "-A")); err != nil {
		return err
	}
	if res, err := git("diff", "--cached", "--quiet"); err != nil {
		return err
	} else if res.ExitCode == 0 {
		return nil // nothing changed
	}
	args := []string{"-c", "commit.gpgsign=false"}
	if res, err := git("config", "user.email"); err != nil {
		return err
	} else if strings.TrimSpace(string(res.Stdout)) == "" {
		args = append(args, "-c", "user.name=axai", "-c", "user.email=axai@localhost")
	}
	return check(git(append(args, "commit", "-q", "-m", message)...))
}

func check(res execx.Result, err error) error {
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("git: exit %d: %s", res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	return nil
}

func sameDir(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}
