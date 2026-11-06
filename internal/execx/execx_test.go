package execx_test

import (
	"bytes"
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/axonops/axai/internal/execx"
)

func TestRunCapturesOutputAndExitCode(t *testing.T) {
	var live bytes.Buffer
	res, err := execx.OS{}.Run(context.Background(), execx.Cmd{
		Name:   "sh",
		Args:   []string{"-c", `echo "out $FOO"; echo err >&2; exit 3`},
		Env:    []string{"FOO=bar"},
		Stdout: &live,
	})
	require.NoError(t, err)
	require.Equal(t, 3, res.ExitCode)
	require.Equal(t, "out bar\n", string(res.Stdout))
	require.Equal(t, "out bar\n", live.String())
	require.Equal(t, "err\n", string(res.Stderr))
}

func TestRunWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	res, err := execx.OS{}.Run(context.Background(), execx.Cmd{Name: "pwd", Dir: dir})
	require.NoError(t, err)
	require.Contains(t, string(res.Stdout), dir[len(dir)-10:])
}

func TestRunMissingBinary(t *testing.T) {
	_, err := execx.OS{}.Run(context.Background(), execx.Cmd{Name: "definitely-not-a-binary-axai"})
	require.ErrorContains(t, err, "definitely-not-a-binary-axai")
}

func TestRunCancelled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := execx.OS{}.Run(ctx, execx.Cmd{Name: "sleep", Args: []string{"5"}})
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestRunCancelKillsProcessGroup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := execx.OS{}.Run(ctx, execx.Cmd{Name: "sh", Args: []string{"-c", "sleep 30 & wait"}})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(start), 5*time.Second)
}

func TestLookPath(t *testing.T) {
	p, err := execx.OS{}.LookPath("sh")
	require.NoError(t, err)
	require.NotEmpty(t, p)
}

func TestRunUnsetRemovesParentVariable(t *testing.T) {
	t.Setenv("AXAI_EXECX_SECRET", "s3cret")
	res, err := execx.OS{}.Run(context.Background(), execx.Cmd{
		Name:  "sh",
		Args:  []string{"-c", `echo "${AXAI_EXECX_SECRET-unset} ${KEEP-unset}"`},
		Env:   []string{"KEEP=yes"},
		Unset: []string{"AXAI_EXECX_SECRET"},
	})
	require.NoError(t, err)
	require.Equal(t, "unset yes\n", string(res.Stdout))
}

func TestRunStartsNewSession(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}
	res, err := execx.OS{}.Run(context.Background(), execx.Cmd{
		Name: py,
		Args: []string{"-c", "import os; print(os.getsid(0) == os.getpid())"},
	})
	require.NoError(t, err)
	require.Equal(t, "True\n", string(res.Stdout))
}
