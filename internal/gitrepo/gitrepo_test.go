package gitrepo_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/axonops/axai/internal/execx"
	"github.com/axonops/axai/internal/execx/exectest"
	"github.com/axonops/axai/internal/gitrepo"
)

// isolate gives git an empty home, so the developer's identity and signing
// settings cannot leak into the tests.
func isolate(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	return home
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	require.NoError(t, err, string(out))
	return strings.TrimSpace(string(out))
}

func project(t *testing.T, parent string) string {
	t.Helper()
	dir := filepath.Join(parent, "kafka")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "site.yml"), []byte("- hosts: kafka\n"), 0o600))
	return dir
}

func TestCommitInitialisesAndCommits(t *testing.T) {
	isolate(t)
	dir := project(t, t.TempDir())
	var log bytes.Buffer
	r := gitrepo.Repo{Exec: execx.OS{}, Dir: dir, Log: &log}
	require.NoError(t, r.Commit(context.Background(), "axai plan: kafka on k1"))
	require.Equal(t, "axai axai@localhost axai plan: kafka on k1", git(t, dir, "log", "--format=%an %ae %s"))

	// Nothing changed: no empty commit.
	require.NoError(t, r.Commit(context.Background(), "axai apply: applied"))
	require.Equal(t, "1", git(t, dir, "rev-list", "--count", "HEAD"))
	require.Empty(t, log.String())
}

func TestCommitUsesTheUserIdentityAndNeverSigns(t *testing.T) {
	home := isolate(t)
	require.NoError(t, os.WriteFile(filepath.Join(home, ".gitconfig"),
		[]byte("[user]\n\tname = Jo Bloggs\n\temail = jo@example.com\n[commit]\n\tgpgsign = true\n"), 0o600))
	dir := project(t, t.TempDir())
	require.NoError(t, gitrepo.Repo{Exec: execx.OS{}, Dir: dir, Log: &bytes.Buffer{}}.Commit(context.Background(), "axai plan: x"))
	require.Equal(t, "Jo Bloggs jo@example.com", git(t, dir, "log", "--format=%an %ae"))
}

func TestCommitInsideAnotherRepository(t *testing.T) {
	isolate(t)
	outer := t.TempDir()
	git(t, outer, "init", "-q")
	dir := project(t, outer)
	var log bytes.Buffer
	require.NoError(t, gitrepo.Repo{Exec: execx.OS{}, Dir: dir, Log: &log}.Commit(context.Background(), "axai plan: x"))
	require.NoDirExists(t, filepath.Join(dir, ".git"))
	require.Contains(t, log.String(), "inside the git repository")
	require.Contains(t, log.String(), "not committing")
}

func TestCommitWithoutGit(t *testing.T) {
	var log bytes.Buffer
	f := &exectest.Fake{}
	require.ErrorIs(t, gitrepo.Repo{Exec: f, Dir: t.TempDir(), Log: &log}.Commit(context.Background(), "x"), gitrepo.ErrNoGit)
	require.Empty(t, log.String())
	require.Empty(t, f.Calls)
}

func TestCommitHidesSecretsFromGit(t *testing.T) {
	f := &exectest.Fake{
		Paths: map[string]string{"git": "/usr/bin/git"},
		Handlers: map[string]exectest.Handler{"git": func(execx.Cmd) (execx.Result, error) {
			return execx.Result{ExitCode: 128}, nil // not a repository, and init fails
		}},
	}
	err := gitrepo.Repo{Exec: f, Dir: t.TempDir(), Log: &bytes.Buffer{}}.Commit(context.Background(), "x")
	require.ErrorContains(t, err, "git")
	for _, c := range f.Calls {
		require.Equal(t, execx.SecretEnv, c.Unset)
		require.Equal(t, "0", exectest.EnvValue(c, "GIT_TERMINAL_PROMPT"))
	}
}
