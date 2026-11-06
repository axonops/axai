package design_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/axonops/axai/internal/design"
	"github.com/axonops/axai/internal/llm/llmtest"
	"github.com/axonops/axai/internal/read"
	"github.com/axonops/axai/internal/spec"
)

const good = `{"resources":[{"kind":"metric_alert_rule","name":"High Disk","action":"create","fields":{"dashboard":"System","chart":"Disk usage %","operator":">","warning_value":70,"critical_value":80,"duration":"15m"}}],"assumptions":["warning at 70"],"unsupported":[]}`

func in() design.Input {
	return design.Input{Request: "alert on disk over 80%", Org: "mycompany", Cluster: "prod", ClusterType: "cassandra", Backend: spec.Terraform}
}

func TestGenerate(t *testing.T) {
	p := &llmtest.Scripted{Steps: []llmtest.Step{llmtest.Text(good)}}
	s, err := design.Generate(context.Background(), p, in())
	require.NoError(t, err)
	require.Equal(t, "mycompany", s.Org)
	require.Len(t, s.Resources, 1)
	require.Equal(t, []string{"warning at 70"}, s.Assumptions)
}

func TestGuardViolationIsFedBack(t *testing.T) {
	bad := `{"resources":[{"kind":"metric_alert_rule","name":"X","action":"create","fields":{"dashboard":"System"}}],"assumptions":[],"unsupported":[]}`
	p := &llmtest.Scripted{Steps: []llmtest.Step{llmtest.Text(bad), llmtest.Text(good)}}
	_, err := design.Generate(context.Background(), p, in())
	require.NoError(t, err)
	require.Len(t, p.Calls, 2)
	require.Contains(t, p.Calls[1].Messages[len(p.Calls[1].Messages)-1].Content, `required field "chart"`)
}

func TestUnknownKindNeverReturned(t *testing.T) {
	bad := `{"resources":[{"kind":"aws_s3_bucket","name":"b","action":"create","fields":{}}],"assumptions":[],"unsupported":[]}`
	p := &llmtest.Scripted{Steps: []llmtest.Step{llmtest.Text(bad), llmtest.Text(bad), llmtest.Text(bad)}}
	_, err := design.Generate(context.Background(), p, in())
	require.Error(t, err)
}

func TestSystemPromptListsClusterCharts(t *testing.T) {
	i := in()
	i.Context = read.Context{Dashboards: []read.Dashboard{{Name: "System", Charts: []string{"Disk usage %"}}}}
	sys := design.System(i)
	require.Contains(t, sys, "System: Disk usage %")
	require.Contains(t, sys, "metric_alert_rule")
	require.Contains(t, sys, "topic:")
	i.Backend = spec.Ansible
	require.NotContains(t, design.System(i), "topic:")
}

func TestUnsupportedPartsStopTheRun(t *testing.T) {
	reply := `{"resources":[{"kind":"metric_alert_rule","name":"High Disk","action":"create","fields":{"dashboard":"System","chart":"Disk usage %","operator":">","warning_value":70,"critical_value":80,"duration":"15m"}}],"assumptions":[],"unsupported":["send an SMS"]}`
	p := &llmtest.Scripted{Steps: []llmtest.Step{llmtest.Text(reply)}}
	_, err := design.Generate(context.Background(), p, in())
	var ue *design.UnsupportedError
	require.ErrorAs(t, err, &ue)
	require.Equal(t, []string{"send an SMS"}, ue.Parts)
}

func TestSystemPromptNamesRequestSecrets(t *testing.T) {
	i := in()
	i.Secrets = []string{"REQUEST_URL_1"}
	sys := design.System(i)
	require.Contains(t, sys, `"$secret:REQUEST_URL_1"`)
	require.Contains(t, sys, "slack_integration")
	require.Contains(t, sys, "secret reference only")
}
