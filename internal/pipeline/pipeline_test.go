package pipeline_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/axonops/axai/internal/execx"
	"github.com/axonops/axai/internal/execx/exectest"
	"github.com/axonops/axai/internal/pipeline"
	"github.com/axonops/axai/internal/runner"
	"github.com/axonops/axai/internal/spec"
	"github.com/axonops/axai/internal/state"
)

func alert(b spec.Backend) spec.Spec {
	return spec.Spec{
		Backend: b, Org: "mycompany", Cluster: "prod", ClusterType: "cassandra",
		Resources: []spec.Resource{{Kind: "metric_alert_rule", Name: "High Disk", Action: spec.Create, Fields: map[string]any{
			"dashboard": "System", "chart": "Disk usage %", "operator": ">", "warning_value": 70.0, "critical_value": 80.0, "duration": "15m",
		}}},
	}
}

// tfFake scripts terraform: plan exit code and a sequence of apply exit codes.
func tfFake(planCode int, applyCodes ...int) *exectest.Fake {
	n := 0
	return &exectest.Fake{
		Paths: map[string]string{"terraform": "/bin/terraform"},
		Handlers: map[string]exectest.Handler{"terraform": func(c execx.Cmd) (execx.Result, error) {
			switch c.Args[0] {
			case "plan":
				return execx.Result{ExitCode: planCode, Stdout: []byte("plan")}, nil
			case "apply":
				code := applyCodes[min(n, len(applyCodes)-1)]
				n++
				return execx.Result{ExitCode: code, Stderr: []byte("provider error")}, nil
			}
			return execx.Result{}, nil
		}},
	}
}

func opts(t *testing.T, f *exectest.Fake, s spec.Spec) pipeline.Options {
	dir := filepath.Join(t.TempDir(), "p")
	return pipeline.Options{
		Dir: dir, Request: "r", Spec: s, Out: &bytes.Buffer{}, MaxFixes: 2,
		Runner: runner.Runner{Exec: f, Backend: s.Backend, Dir: dir},
	}
}

func status(t *testing.T, dir string) string {
	st, ok, err := state.Load(dir)
	require.NoError(t, err)
	require.True(t, ok)
	return st.Status
}

func TestPlanWritesProject(t *testing.T) {
	o := opts(t, tfFake(2), alert(spec.Terraform))
	out, err := pipeline.Plan(context.Background(), o)
	require.NoError(t, err)
	require.Equal(t, pipeline.Planned, out)
	require.FileExists(t, filepath.Join(o.Dir, "main.tf"))
	require.Equal(t, "planned", status(t, o.Dir))
}

func TestPlanRejectsInvalidSpecWithoutWriting(t *testing.T) {
	s := alert(spec.Terraform)
	s.Resources[0].Kind = "aws_s3_bucket"
	o := opts(t, tfFake(2), s)
	_, err := pipeline.Plan(context.Background(), o)
	require.Error(t, err)
	_, statErr := os.Stat(o.Dir)
	require.True(t, os.IsNotExist(statErr))
}

func TestNoChanges(t *testing.T) {
	o := opts(t, tfFake(0), alert(spec.Terraform))
	o.Yes = true
	out, err := pipeline.Apply(context.Background(), o)
	require.NoError(t, err)
	require.Equal(t, pipeline.NoChanges, out)
}

func TestApplyYes(t *testing.T) {
	f := tfFake(2, 0)
	o := opts(t, f, alert(spec.Terraform))
	o.Yes = true
	out, err := pipeline.Apply(context.Background(), o)
	require.NoError(t, err)
	require.Equal(t, pipeline.Applied, out)
	require.Equal(t, "applied", status(t, o.Dir))
	require.Len(t, f.CallsTo("terraform"), 4, "init, fmt, plan, apply")
}

func TestDeclineAppliesNothing(t *testing.T) {
	f := tfFake(2, 0)
	o := opts(t, f, alert(spec.Terraform))
	o.Confirm = func(string) (bool, error) { return false, nil }
	out, err := pipeline.Apply(context.Background(), o)
	require.NoError(t, err)
	require.Equal(t, pipeline.Declined, out)
	require.Equal(t, "planned", status(t, o.Dir))
	for _, c := range f.CallsTo("terraform") {
		require.NotEqual(t, "apply", c.Args[0])
	}
}

func TestDestructiveAsksEvenWithYes(t *testing.T) {
	s := alert(spec.Ansible)
	s.Resources[0].Action = spec.Delete
	f := &exectest.Fake{
		Paths: map[string]string{"ansible-playbook": "/b/ansible-playbook", "ansible-galaxy": "/b/ansible-galaxy"},
		Handlers: map[string]exectest.Handler{
			"ansible-galaxy":   func(execx.Cmd) (execx.Result, error) { return execx.Result{}, nil },
			"ansible-playbook": func(execx.Cmd) (execx.Result, error) { return execx.Result{Stdout: []byte("changed=1")}, nil },
		},
	}
	o := opts(t, f, s)
	o.Yes = true
	asked := false
	o.Confirm = func(string) (bool, error) { asked = true; return false, nil }
	out, err := pipeline.Apply(context.Background(), o)
	require.NoError(t, err)
	require.True(t, asked)
	require.Equal(t, pipeline.Declined, out)
}

func TestFixerLoopStopsAtLimit(t *testing.T) {
	f := tfFake(2, 1) // apply always fails
	o := opts(t, f, alert(spec.Terraform))
	o.Yes = true
	o.MaxFixes = 2
	calls := 0
	o.Reviser = func(_ context.Context, cur spec.Spec, _ string) (spec.Spec, error) { calls++; return cur, nil }
	_, err := pipeline.Apply(context.Background(), o)
	require.ErrorIs(t, err, pipeline.ErrApplyFailed)
	require.Equal(t, 2, calls)
	require.Equal(t, "failed", status(t, o.Dir))
}

func TestFixerRepairs(t *testing.T) {
	f := tfFake(2, 1, 0) // fail then succeed
	o := opts(t, f, alert(spec.Terraform))
	o.Yes = true
	o.Reviser = func(_ context.Context, cur spec.Spec, _ string) (spec.Spec, error) {
		next := cur
		next.Resources = []spec.Resource{cur.Resources[0]}
		fields := map[string]any{}
		for k, v := range cur.Resources[0].Fields {
			fields[k] = v
		}
		fields["duration"] = "20m"
		next.Resources[0].Fields = fields
		return next, nil
	}
	out, err := pipeline.Apply(context.Background(), o)
	require.NoError(t, err)
	require.Equal(t, pipeline.Applied, out)
	st, _, _ := state.Load(o.Dir)
	require.Len(t, st.Fixes, 1)
	require.Equal(t, []string{"main.tf"}, st.Fixes[0].Files)
}
