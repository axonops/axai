package guard_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/axonops/axai/internal/guard"
	"github.com/axonops/axai/internal/spec"
)

func valid() spec.Spec {
	return spec.Spec{
		Backend: spec.Terraform, Org: "mycompany", Cluster: "prod", ClusterType: "cassandra",
		Resources: []spec.Resource{{
			Kind: "metric_alert_rule", Name: "High Disk", Action: spec.Create,
			Fields: map[string]any{
				"dashboard": "System", "chart": "Disk usage %", "operator": ">",
				"warning_value": 70.0, "critical_value": 80.0, "duration": "15m",
			},
		}},
	}
}

func TestValidSpec(t *testing.T) {
	require.NoError(t, guard.Validate(valid()))
}

func TestRejections(t *testing.T) {
	cases := map[string]struct {
		mutate func(*spec.Spec)
		want   string
	}{
		"unknown kind":     {func(s *spec.Spec) { s.Resources[0].Kind = "aws_s3_bucket" }, "unknown resource kind"},
		"bad backend":      {func(s *spec.Spec) { s.Backend = "pulumi" }, "backend"},
		"empty":            {func(s *spec.Spec) { s.Resources = nil }, "no resources"},
		"missing required": {func(s *spec.Spec) { delete(s.Resources[0].Fields, "chart") }, `required field "chart"`},
		"unknown field":    {func(s *spec.Spec) { s.Resources[0].Fields["foo"] = "x" }, `unknown field "foo"`},
		"wrong type":       {func(s *spec.Spec) { s.Resources[0].Fields["warning_value"] = "high" }, "want number"},
		"bad operator":     {func(s *spec.Spec) { s.Resources[0].Fields["operator"] = "~" }, "not one of"},
		"bad duration":     {func(s *spec.Spec) { s.Resources[0].Fields["duration"] = "soon" }, "duration"},
		"inline credential": {func(s *spec.Spec) {
			s.Resources[0].Fields["description"] = "https://hooks.slack.com/services/T0/B0/xyz"
		}, "inline credential"},
		"duplicate": {func(s *spec.Spec) {
			s.Resources = append(s.Resources, s.Resources[0])
		}, "duplicate"},
		"terraform-only field on ansible": {func(s *spec.Spec) {
			s.Backend = spec.Ansible
			s.Resources[0].Fields["topic"] = []any{"orders"}
		}, "not supported by the ansible backend"},
		"kafka field on bad action": {func(s *spec.Spec) { s.Resources[0].Action = "explode" }, "action"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s := valid()
			tc.mutate(&s)
			err := guard.Validate(s)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestDeleteNeedsOnlyName(t *testing.T) {
	s := valid()
	s.Backend = spec.Ansible
	s.Resources[0].Action = spec.Delete
	s.Resources[0].Fields = map[string]any{}
	require.NoError(t, guard.Validate(s))
}

func TestTerraformDeleteRejected(t *testing.T) {
	s := valid()
	s.Resources[0].Action = spec.Delete
	require.ErrorContains(t, guard.Validate(s), "delete is only supported by the ansible backend")
}

func dashboard(panels ...any) spec.Spec {
	return spec.Spec{
		Backend: spec.Terraform, Org: "mycompany", Cluster: "axonops", ClusterType: "cassandra",
		Resources: []spec.Resource{{Kind: "custom_dashboard", Name: "Capacity", Action: spec.Create,
			Fields: map[string]any{"panels": panels}}},
	}
}

func counter() map[string]any {
	return map[string]any{"title": "Cluster free space %", "type": "metrics_counter", "query": "100 - avg(host_Disk_UsedPercent{mountpoint='/'})"}
}

func TestDashboardValid(t *testing.T) {
	require.NoError(t, guard.Validate(dashboard(counter())))
}

func TestDashboardRejections(t *testing.T) {
	cases := map[string]struct {
		panel map[string]any
		want  string
	}{
		"bad type":  {map[string]any{"title": "a", "type": "gauge", "query": "x"}, "not one of"},
		"no query":  {map[string]any{"title": "a", "type": "metrics_counter"}, "query is required"},
		"row query": {map[string]any{"title": "a", "type": "row", "query": "x"}, "a row has no query"},
		"unknown":   {map[string]any{"title": "a", "type": "line-chart", "query": "x", "x": 1.0}, "unknown key"},
		"reserved":  {map[string]any{"title": "__EMPTY_ROW__", "type": "row"}, "reserved"},
		"wide":      {map[string]any{"title": "a", "type": "line-chart", "query": "x", "w": 30.0}, "from 1 to 18"},
		"no title":  {map[string]any{"type": "line-chart", "query": "x"}, "title is required"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := guard.Validate(dashboard(tc.panel))
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestDashboardNotOnAnsibleYet(t *testing.T) {
	s := dashboard(counter())
	s.Backend = spec.Ansible
	require.ErrorContains(t, guard.Validate(s), "not supported by the ansible backend")
}
