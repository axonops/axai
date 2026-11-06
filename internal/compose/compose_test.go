package compose_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/axonops/axai/internal/compose"
	"github.com/axonops/axai/internal/spec"
)

func alert() spec.Spec {
	return spec.Spec{
		Org: "mycompany", Cluster: "prod", ClusterType: "cassandra",
		Resources: []spec.Resource{{
			Kind: "metric_alert_rule", Name: "High Disk", Action: spec.Create,
			Fields: map[string]any{
				"dashboard": "System", "chart": "Disk usage %", "operator": "=",
				"warning_value": 70.0, "critical_value": 80.0, "duration": "15m",
				"description": "Disk is filling up", "group_by": []any{"host_id"},
			},
		}},
	}
}

func TestTerraform(t *testing.T) {
	s := alert()
	s.Backend = spec.Terraform
	f, err := compose.Render(s)
	require.NoError(t, err)
	require.Contains(t, f["versions.tf"], `source  = "axonops/axonops"`)
	require.Contains(t, f["main.tf"], `resource "axonops_metric_alert_rule" "high_disk" {`)
	require.Contains(t, f["main.tf"], `cluster_name   = "prod"`)
	require.Contains(t, f["main.tf"], `warning_value  = 70`)
	require.Contains(t, f["main.tf"], "annotations = {\n    description = \"Disk is filling up\"\n  }")
	require.Contains(t, f["main.tf"], `group_by       = ["host_id"]`)
	require.Contains(t, f["variables.tf"], "sensitive   = true")
}

func TestTerraformDeterministic(t *testing.T) {
	s := alert()
	s.Backend = spec.Terraform
	a, _ := compose.Render(s)
	b, _ := compose.Render(s)
	require.Equal(t, a, b)
}

func TestTerraformEscapesInterpolation(t *testing.T) {
	s := alert()
	s.Backend = spec.Terraform
	s.Resources[0].Fields["description"] = "cost ${x} %{if}"
	f, err := compose.Render(s)
	require.NoError(t, err)
	require.Contains(t, f["main.tf"], `cost $${x} %%{if}`)
}

func TestTerraformRejectsDelete(t *testing.T) {
	s := alert()
	s.Backend = spec.Terraform
	s.Resources[0].Action = spec.Delete
	_, err := compose.Render(s)
	require.ErrorContains(t, err, "delete is not supported")
}

func TestAnsible(t *testing.T) {
	s := alert()
	s.Backend = spec.Ansible
	f, err := compose.Render(s)
	require.NoError(t, err)
	require.Contains(t, f["site.yml"], "axonops.axonops.configurations")
	require.Contains(t, f["group_vars/all/axonops.yml"], "axonops_alert_rules:")
	require.Contains(t, f["group_vars/all/axonops.yml"], "operator: ==")
	require.Contains(t, f["group_vars/all/axonops.yml"], "present: true")
	require.Contains(t, f["requirements.yml"], "axonops.axonops")
}

func TestAnsibleDelete(t *testing.T) {
	s := alert()
	s.Backend = spec.Ansible
	s.Resources[0].Action = spec.Delete
	f, err := compose.Render(s)
	require.NoError(t, err)
	require.Contains(t, f["group_vars/all/axonops.yml"], "present: false")
}

func TestTerraformCounterDashboard(t *testing.T) {
	s := spec.Spec{
		Backend: spec.Terraform, Org: "mycompany", Cluster: "axonops", ClusterType: "cassandra",
		Resources: []spec.Resource{{Kind: "custom_dashboard", Name: "Capacity", Action: spec.Create,
			Fields: map[string]any{"panels": []any{
				map[string]any{"title": "Cluster free space %", "type": "metrics_counter", "query": "100 - avg(host_Disk_UsedPercent{mountpoint='/'})"},
			}}}},
	}
	f, err := compose.Render(s)
	require.NoError(t, err)
	main := f["main.tf"]
	require.Contains(t, main, `resource "axonops_custom_dashboard" "capacity" {`)
	require.Contains(t, main, `"metrics_counter"`)
	require.Contains(t, main, `jsonencode({ y = {}, queries = [{ query = "100 - avg(host_Disk_UsedPercent{mountpoint='/'})", legend = "" }] })`)
	require.Contains(t, main, "layout = {")
	require.Contains(t, main, "w = 6")
}

// TestTerraformValidates renders a dashboard and checks it with
// `terraform validate`. It needs terraform and network access to the
// registry, so it runs only when AXAI_TF_VALIDATE=1.
func TestTerraformValidates(t *testing.T) {
	if os.Getenv("AXAI_TF_VALIDATE") != "1" {
		t.Skip("set AXAI_TF_VALIDATE=1 to run")
	}
	bin, err := exec.LookPath("terraform")
	if err != nil {
		t.Skip("terraform not installed")
	}
	s := spec.Spec{
		Backend: spec.Terraform, Org: "mycompany", Cluster: "axonops", ClusterType: "cassandra",
		Resources: []spec.Resource{{Kind: "custom_dashboard", Name: "Capacity", Action: spec.Create,
			Fields: map[string]any{"panels": []any{
				map[string]any{"title": "Capacity", "type": "row"},
				map[string]any{"title": "Cluster free space %", "type": "metrics_counter", "query": "100 - avg(host_Disk_UsedPercent{mountpoint='/'})"},
				map[string]any{"title": "Disk used %", "type": "line-chart", "query": "host_Disk_UsedPercent", "legend": "{{host_id}}"},
			}}}},
	}
	s.Resources = append(s.Resources, slackSpec().Resources...)
	files, err := compose.Render(s)
	require.NoError(t, err)
	dir := t.TempDir()
	for name, c := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(c), 0o600))
	}
	for _, args := range [][]string{{"init", "-backend=false", "-input=false"}, {"validate"}} {
		cmd := exec.Command(bin, args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}
	t.Log(files["main.tf"])
}

func slackSpec() spec.Spec {
	return spec.Spec{
		Backend: spec.Terraform, Org: "mycompany", Cluster: "axonops", ClusterType: "cassandra",
		Resources: []spec.Resource{
			{Kind: "alert_route", Name: "disk to slack", Action: spec.Create, Fields: map[string]any{
				"integration_name": "ops-slack", "integration_type": "slack", "type": "metrics", "severity": "error"}},
			{Kind: "slack_integration", Name: "ops-slack", Action: spec.Create, Fields: map[string]any{
				"webhook_url": "$secret:REQUEST_URL_1", "channel": "#ops"}},
		},
	}
}

func TestSlackIntegrationUsesSecretVariable(t *testing.T) {
	f, err := compose.Render(slackSpec())
	require.NoError(t, err)
	require.Contains(t, f["main.tf"], "webhook_url  = var.request_url_1")
	require.Contains(t, f["variables.tf"], `variable "request_url_1"`)
	require.NotContains(t, f["main.tf"], "hooks.slack.com")
}

func TestRouteReferencesIntegration(t *testing.T) {
	f, err := compose.Render(slackSpec())
	require.NoError(t, err)
	require.Contains(t, f["main.tf"], "integration_name = axonops_slack_integration.ops_slack.name")
}
