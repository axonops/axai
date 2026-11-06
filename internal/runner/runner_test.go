package runner_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/axonops/axai/internal/execx"
	"github.com/axonops/axai/internal/execx/exectest"
	"github.com/axonops/axai/internal/runner"
	"github.com/axonops/axai/internal/spec"
)

func fake(code int, out string) *exectest.Fake {
	h := func(execx.Cmd) (execx.Result, error) { return execx.Result{Stdout: []byte(out), ExitCode: code}, nil }
	return &exectest.Fake{
		Paths:    map[string]string{"terraform": "/bin/terraform", "ansible-playbook": "/bin/ansible-playbook"},
		Handlers: map[string]exectest.Handler{"terraform": h, "ansible-playbook": h},
	}
}

func TestTerraformPlanExitCodes(t *testing.T) {
	for code, changes := range map[int]bool{0: false, 2: true} {
		r := runner.Runner{Exec: fake(code, "plan"), Backend: spec.Terraform, Dir: "/p"}
		res, err := r.Plan(context.Background())
		require.NoError(t, err)
		require.Equal(t, changes, res.Changes)
	}
	r := runner.Runner{Exec: fake(1, "boom"), Backend: spec.Terraform, Dir: "/p"}
	_, err := r.Plan(context.Background())
	var f *runner.Failure
	require.ErrorAs(t, err, &f)
}

func TestAnsiblePlanDetectsChanges(t *testing.T) {
	r := runner.Runner{Exec: fake(0, "localhost : ok=3 changed=1 failed=0"), Backend: spec.Ansible, Dir: "/p"}
	res, err := r.Plan(context.Background())
	require.NoError(t, err)
	require.True(t, res.Changes)
	r = runner.Runner{Exec: fake(0, "localhost : ok=3 changed=0 failed=0"), Backend: spec.Ansible, Dir: "/p"}
	res, _ = r.Plan(context.Background())
	require.False(t, res.Changes)
}

func TestMissingTool(t *testing.T) {
	r := runner.Runner{Exec: &exectest.Fake{}, Backend: spec.Terraform}
	require.ErrorIs(t, r.Init(context.Background()), runner.ErrToolMissing)
}

func TestEnvPassed(t *testing.T) {
	f := fake(0, "")
	r := runner.Runner{Exec: f, Backend: spec.Terraform, Env: []string{"AXONOPS_API_KEY=k"}}
	_, _ = r.Plan(context.Background())
	require.Equal(t, "k", exectest.EnvValue(f.CallsTo("terraform")[0], "AXONOPS_API_KEY"))
}
