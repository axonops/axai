package catalogue

func init() {
	register(Kind{
		Name:         "slack_integration",
		ClusterTypes: []string{"cassandra", "dse", "kafka"},
		Terraform:    "axonops_slack_integration",
		Identity:     "name",
		Notes: "A Slack webhook AxonOps sends alerts to. It sends nothing until an alert_route points at it. " +
			"webhook_url must be a $secret: reference; if the request gave a URL, use the secret named for it.",
		Fields: []Field{
			{Name: "name", Type: String, Required: true},
			{Name: "webhook_url", Type: String, Required: true, Secret: true},
			{Name: "channel", Type: String},
		},
	})
	register(Kind{
		Name:         "alert_route",
		ClusterTypes: []string{"cassandra", "dse", "kafka"},
		Terraform:    "axonops_alert_route",
		Notes: "Sends alerts of one type and severity to an integration. The resource name is only a label. " +
			"integration_name is the name of a slack_integration (or other integration) and must match it exactly. " +
			"Route severity error to cover critical alerts. type metrics covers metric alert rules.",
		Fields: []Field{
			{Name: "integration_name", Type: String, Required: true},
			{Name: "integration_type", Type: String, Required: true, Enum: []string{"email", "smtp", "pagerduty", "slack", "teams", "servicenow", "webhook", "opsgenie"}},
			{Name: "type", Type: String, Required: true, Enum: []string{"global", "metrics", "backups", "servicechecks", "nodes", "commands", "repairs", "rollingrestart"}},
			{Name: "severity", Type: String, Required: true, Enum: []string{"info", "warning", "error"}},
			{Name: "enable_override", Type: Bool},
		},
	})
}
