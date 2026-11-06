package catalogue

func init() {
	register(Kind{
		Name:         "metric_alert_rule",
		ClusterTypes: []string{"cassandra", "dse", "kafka"},
		Terraform:    "axonops_metric_alert_rule",
		AnsibleVar:   "axonops_alert_rules",
		Identity:     "name",
		TerraformFields: func(f map[string]any) map[string]any {
			out := map[string]any{}
			for k, v := range f {
				if k == "description" {
					continue
				}
				out[k] = v
			}
			if d, ok := f["description"]; ok {
				out["annotations"] = map[string]any{"description": d}
			}
			return out
		},
		AnsibleFields: func(f map[string]any) map[string]any {
			out := map[string]any{}
			for k, v := range f {
				out[k] = v
			}
			if out["operator"] == "=" {
				out["operator"] = "=="
			}
			return out
		},
		Fields: []Field{
			{Name: "name", Type: String, Required: true},
			{Name: "dashboard", Type: String, Required: true},
			{Name: "chart", Type: String, Required: true},
			{Name: "operator", Type: String, Required: true, Enum: []string{">", ">=", "=", "!=", "<=", "<"}},
			{Name: "warning_value", Type: Number, Required: true},
			{Name: "critical_value", Type: Number, Required: true},
			{Name: "duration", Type: String, Required: true},
			{Name: "description", Type: String},
			{Name: "metric", Type: String},
			{Name: "scope", Type: StringList},
			{Name: "dc", Type: StringList},
			{Name: "rack", Type: StringList},
			{Name: "host_id", Type: StringList},
			{Name: "group_by", Type: StringList, Enum: []string{"dc", "host_id", "rack", "scope"}},
			{Name: "percentile", Type: StringList, Enum: []string{"75thPercentile", "95thPercentile", "98thPercentile", "99thPercentile", "999thPercentile"}},
			{Name: "consistency", Type: StringList, Enum: []string{"ALL", "ANY", "ONE", "TWO", "THREE", "SERIAL", "QUORUM", "EACH_QUORUM", "LOCAL_ONE", "LOCAL_QUORUM", "LOCAL_SERIAL"}},
			{Name: "keyspace", Type: StringList},
			{Name: "topic", Type: StringList, Backends: []string{"terraform"}},
			{Name: "group_id", Type: StringList, Backends: []string{"terraform"}},
		},
	})
}
