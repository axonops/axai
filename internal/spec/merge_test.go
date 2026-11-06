package spec_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/axonops/axai/internal/spec"
)

func base() spec.Spec {
	return spec.Spec{Backend: spec.Terraform, Org: "o", Cluster: "c", ClusterType: "cassandra",
		Resources: []spec.Resource{{Kind: "custom_dashboard", Name: "Free", Action: spec.Create, Fields: map[string]any{"panels": []any{"p"}}}}}
}

func delta(rs ...spec.Resource) spec.Spec {
	d := base()
	d.Resources = rs
	return d
}

func alert(action spec.Action, f map[string]any) spec.Resource {
	return spec.Resource{Kind: "metric_alert_rule", Name: "Disk", Action: action, Fields: f}
}

func TestMergeAddsAndKeeps(t *testing.T) {
	out, ch, err := spec.Merge(base(), delta(alert(spec.Create, map[string]any{"duration": "15m"})))
	require.NoError(t, err)
	require.Len(t, out.Resources, 2)
	require.Equal(t, "custom_dashboard", out.Resources[0].Kind)
	require.Equal(t, []spec.Change{{Kind: "metric_alert_rule", Name: "Disk", Op: spec.Added}}, ch)
}

func TestMergeIntoEmptyBase(t *testing.T) {
	out, ch, err := spec.Merge(spec.Spec{}, delta(alert(spec.Create, nil)))
	require.NoError(t, err)
	require.Len(t, out.Resources, 1)
	require.Equal(t, spec.Added, ch[0].Op)
}

func TestCreateOfIdenticalIsNoop(t *testing.T) {
	b := base()
	d := delta(b.Resources[0])
	out, ch, err := spec.Merge(b, d)
	require.NoError(t, err)
	require.Len(t, out.Resources, 1)
	require.Equal(t, spec.Unchanged, ch[0].Op)
}

func TestCreateOfDifferentIsConflictAndNothingChanges(t *testing.T) {
	b := base()
	r := b.Resources[0]
	r.Fields = map[string]any{"panels": []any{"other"}}
	_, _, err := spec.Merge(b, delta(r))
	var ce *spec.ConflictError
	require.ErrorAs(t, err, &ce)
	require.Contains(t, ce.Error(), "ask to update")
	require.Equal(t, []any{"p"}, b.Resources[0].Fields["panels"])
}

func TestUpdateOverridesOnlyGivenFields(t *testing.T) {
	b := base()
	b.Resources = append(b.Resources, alert(spec.Create, map[string]any{"duration": "15m", "operator": ">"}))
	out, ch, err := spec.Merge(b, delta(alert(spec.Update, map[string]any{"duration": "30m"})))
	require.NoError(t, err)
	require.Equal(t, map[string]any{"duration": "30m", "operator": ">"}, out.Resources[1].Fields)
	require.Equal(t, spec.Updated, ch[0].Op)
	require.Equal(t, "15m", b.Resources[1].Fields["duration"], "base is not mutated")
}

func TestUpdateOfMissingIsConflict(t *testing.T) {
	_, _, err := spec.Merge(base(), delta(alert(spec.Update, map[string]any{"duration": "1h"})))
	require.ErrorContains(t, err, "nothing to update")
}

func TestDeleteMarksResource(t *testing.T) {
	out, ch, err := spec.Merge(base(), delta(spec.Resource{Kind: "custom_dashboard", Name: "Free", Action: spec.Delete}))
	require.NoError(t, err)
	require.Equal(t, spec.Delete, out.Resources[0].Action)
	require.Equal(t, spec.Removed, ch[0].Op)
}

func TestDifferentTargetIsRejected(t *testing.T) {
	d := delta(alert(spec.Create, nil))
	d.Cluster = "other"
	_, _, err := spec.Merge(base(), d)
	require.ErrorContains(t, err, "use another --project")
}
