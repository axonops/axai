# axai architecture

Repository: `github.com/axonops/axai`

## 1. Goal

A single Go binary that turns a natural-language request into working
AxonOps configuration by generating and applying Ansible or Terraform code.

```
axai run "Create an alert when disk usage is over 80%"
axai run "Set up common alerts for the cassandra cluster and route them to Slack at https://hooks.slack.com/…"
axai run "Create 10 topics with random names and an ACL allowing only read"
```

The user is never asked clarifying questions. Every gap is resolved from the
catalogue and from reads against the AxonOps API. The only interaction is a
single confirmation of the generated plan, which `--yes` skips.

### Success criteria

- The example prompts above produce valid, applied AxonOps configuration with no
  user input beyond the confirmation.
- The run leaves a self-contained Ansible or Terraform project on disk (committed
  to git when `--git` is given) that can be re-run without the binary or a model.
- Re-running the same prompt against the same cluster reports no changes.
- The same flow works with a hosted model (Anthropic, OpenAI, or the Claude Code
  CLI through `claude-cli`) and a local model (Ollama, llama.cpp server).

### Non-goals

- Installing, upgrading, or operating the AxonOps server itself.
- Writing AxonOps configuration directly via the API — all mutations go through
  generated IaC.
- Managing Cassandra/Kafka cluster infrastructure (nodes, storage, network).
  Clusters must already exist in AxonOps.
- Gemini and Bedrock native adapters (MVP).


## 2. Key decisions

| Decision | Choice | Reason |
|----------|--------|--------|
| Deliverable | IaC project on disk (git) and applied | Auditable, reusable without the binary |
| Mutations | Only through generated Ansible or Terraform | No direct API writes; every change is reviewable |
| Reads | AxonOps API or MCP directly | Read-only access is safe and needed to inform generation |
| Backend selection | User flag `--backend ansible\|terraform`; default `terraform` | The deliverable is a reusable config; the user picks the tool they run |
| Pipeline design | Deterministic Go pipeline; model only at schema-constrained steps | Testable, reliable on small local models |
| Destructive intents | Confirmation gate before any delete, even with `--yes` | Mistakes here are hard to recover from |
| Credentials | Only from environment variables; never written into generated files | DIGITALIS.md secrets rule |


## 3. Resources

### Cassandra (and DSE)

| Intent | Terraform resource | Ansible variable |
|--------|-------------------|-----------------|
| Metric alert rule | `axonops_metric_alert_rule` | `axonops_alert_rules` (metrics) |
| Log alert rule | `axonops_log_alert_rule` | `axonops_alert_rules` (log_alerts) |
| Alert route | `axonops_alert_route` | `axonops_alert_routes` |
| Silence | `axonops_silence` | — |
| Slack integration | `axonops_slack_integration` | `slack_integration` |
| Teams integration | `axonops_teams_integration` | `axonops_teams_integrations` |
| PagerDuty integration | `axonops_pagerduty_integration` | `pagerduty_integration` |
| OpsGenie integration | `axonops_opsgenie_integration` | — |
| ServiceNow integration | `axonops_servicenow_integration` | — |
| Custom dashboard | `axonops_custom_dashboard` | `axonops_dashboard_templates` |
| Adaptive repair | `axonops_cassandra_adaptive_repair` | `adaptive_repair` |
| Scheduled repair | `axonops_cassandra_scheduled_repair` | — |
| Backup | `axonops_cassandra_backup` | `axonops_backups` |
| Commitlog archive | `axonops_cassandra_commitlog_settings` | `axonops_commitlog_archive` |
| TCP health check | `axonops_healthcheck_tcp` | `axonops_tcp_check` |
| HTTP health check | `axonops_healthcheck_http` | — |
| Shell health check | `axonops_healthcheck_shell` | `axonops_shell_check` |
| Log collector | `axonops_logcollector` | `logcollector` |
| Agent disconnection tolerance | `axonops_cassandra_agent_disconnect_tolerance` | `agent_disconnection_tolerance` |

### Kafka

| Intent | Terraform resource | Ansible variable |
|--------|-------------------|-----------------|
| Topic | `axonops_kafka_topic` | — |
| ACL | `axonops_kafka_acl` | — |
| Connect connector | `axonops_kafka_connect_connector` | — |
| Schema | `axonops_schema` | — |
| Schema registry compatibility | `axonops_schema_registry_compatibility` | — |
| Metric alert rule | `axonops_metric_alert_rule` | `axonops_alert_rules` (metrics) |
| Log alert rule | `axonops_log_alert_rule` | `axonops_alert_rules` (log_alerts) |

### Read-only (inform generation, never mutated by axai)

AxonOps data sources — `axonops_clusters`, `axonops_cluster_nodes`,
`axonops_cassandra_keyspaces`, `axonops_kafka_broker_config`, etc. — are queried
at intake to populate cluster context. The AxonOps MCP server may also be used
for richer metric-aware requests (e.g. "adjust alerts for the current metrics").


## 4. Pipeline

```
prompt ──▶ 1  Intake       NL → structured Request (action, resource, cluster, org, backend)
           2  Read         AxonOps API → cluster context (keyspaces, topics, existing alerts…)
           3  Catalogue    vetted template pack? (e.g. "common cassandra alerts")
           4  Design       Model: Request + context + catalogue → IaC spec (untrusted)
           5  Guard        schema / allowlist check on generated IaC
           6  Compose      write project dir (Terraform or Ansible)
           7  Plan         terraform plan / ansible --check → show diff to user
           8  Confirm      user reviews (skipped with --yes; destructive always asks)
           9  Apply        terraform apply / ansible-playbook with bounded fixer loop
          10  State        record outcome + optionally git-commit the project
```

### Stage notes

**1 Intake.** The model extracts: action (`create`, `update`, `delete`),
resource kinds, target org, cluster name, backend (`ansible` | `terraform`).
Every field is checked against the resource table above — the model must not
invent resource types. `ErrEmptyPrompt` is returned when the request is blank.

**2 Read.** Short, read-only API calls. No write occurs here. When the prompt
references current state ("adjust for current metrics", "using the MCP") this
stage enriches the request with live data before design.

**3 Catalogue.** Vetted template packs bundled with axai. Examples:

- `cassandra-common-alerts` — standard disk, GC, latency, and repair alert rules
- `kafka-common-alerts` — consumer-lag, under-replicated-partitions, broker health

If the request matches a catalogue entry the design stage uses the template as a
base, reducing model generation surface. Unknown resource combinations fall
through to free-form design.

**4 Design.** One model call. The model is given a system prompt that lists every
rule guard enforces (valid resource names, required fields, no secrets inline,
no extra providers). It returns a structured spec: a list of resources with their
fields, plus an `assumptions` list for every gap it filled. The model output is
treated as untrusted until guard passes.

**5 Guard.** Deterministic Go code. Checks:
- Resource kinds are in the allowed set for the backend.
- Required fields are present and well-typed.
- No credential values written inline (must be `var.xxx` or `{{ env_var }}`).
- For Terraform: provider is `axonops/axonops` only; no `local-exec` provisioners.
- For Ansible: only `axonops.axonops` collection modules; no `shell` or `command`
  tasks in generated plays.

**6 Compose.** Go writes the project files deterministically from the spec:

- Terraform: `main.tf` (resources), `variables.tf`, `terraform.tfvars.example`,
  `versions.tf` (provider pin).
- Ansible: `group_vars/all/axonops.yml` (vars), `site.yml` importing
  `axonops.axonops.configurations`, `verify.yml`.

**7 Plan.** Runs `terraform plan` or `ansible-playbook --check`. Output is printed
for the user. If the plan shows no changes and the request was not a forced
re-apply, axai exits 0 with "no changes".

**8 Confirm.** User sees the plan diff and types `yes`. Suppressed by `--yes`.
Delete operations (any resource with `present: false` / `count: 0` / `terraform
destroy`) always prompt regardless of `--yes`.

**9 Apply.** Runs `terraform apply -auto-approve` or `ansible-playbook`. On
failure, the fixer stage calls the model with the error output, the generated
files, and the guard rules, asking for a minimal patch. The patched files are
re-validated by guard before being applied. At most `--max-fixes` attempts
(default 3) per run.

**10 State.** Writes `state.json` in the project dir with outcome, timestamp, and
fix log. When `--git` is given, commits the project with a message in the form
`axai: apply cassandra alert-rules for mycluster (3 resources)`.


## 5. CLI

```
axai [flags] <command> [request]

Commands:
  plan    Generate and show the plan without applying
  apply   Plan and apply (alias: run)
  status  Show state of the last run for a project
  doctor  Check prerequisites (terraform/ansible, provider auth)

Flags:
  --org string          AxonOps organisation (or AXONOPS_ORG)
  --cluster string      Cluster name (or AXONOPS_CLUSTER)
  --cluster-type        cassandra|kafka|dse (or AXONOPS_CLUSTER_TYPE)
  --backend             ansible|terraform (default: terraform)
  --project string      Project directory (default: .axai/<hash>)
  --yes                 Skip plan confirmation (destructive still asks)
  --max-fixes int       Max fixer iterations per apply (default: 3)
  --model string        Model provider (anthropic|openai|ollama|claude-cli)
  --git                 Commit the project to git after a successful apply
  --no-colours          Disable colour output
```

### Exit codes

| Code | Meaning |
|------|---------|
| 0 | Success, or no changes |
| 1 | Input error (bad flags, empty prompt, unknown resource) |
| 2 | Validation error (guard failed, plan diff mismatch) |
| 3 | Apply failed after fixer exhausted |
| 4 | Runtime error (API unreachable, model error, internal) |


## 6. Credentials

All credentials come from environment variables and are never written into
generated files. Generated Terraform uses `variable` blocks with `sensitive =
true`; generated Ansible references `{{ lookup('env', 'VAR') }}`.

| Variable | Purpose |
|----------|---------|
| `AXONOPS_ORG` | AxonOps organisation slug |
| `AXONOPS_API_TOKEN` | AxonOps API token (read and write). Optional for `plan` (warns, writes the project, skips the backend plan); required for `apply`. Passed to Terraform as `AXONOPS_API_KEY` and to Ansible as `AXONOPS_API_TOKEN` |
| `AXONOPS_BASE_URL` | Self-hosted server URL. Passed on as `AXONOPS_URL` (Ansible) and `AXONOPS_HOST`/`AXONOPS_PROTOCOL` (Terraform). Unset means AxonOps SaaS |
| `AXONOPS_TOKEN_TYPE` | `Bearer` (SaaS, default) or `AxonApi` (self-hosted) |
| `ANTHROPIC_API_KEY` | Anthropic model (when `--model anthropic`) |
| `OPENAI_API_KEY` | OpenAI model (when `--model openai`) |
| `OLLAMA_HOST` | Ollama base URL (when `--model ollama`) |


## 7. Package layout

Mirrors ansai's structure. New packages replace the Ansible-specific ones.

```
cmd/axai/              binary entry point
internal/
  cli/                 cobra commands: plan, apply, status, doctor
  intake/              NL → Request (model call + validation)
  read/                AxonOps API reads for cluster context
  catalogue/           bundled alert/config template packs
  design/              model call → IaC spec (untrusted)
  guard/               deterministic spec validation
  compose/             spec → project files (terraform or ansible)
  runner/              terraform and ansible runners (exec wrappers)
  fixer/               model patches for failed apply
  state/               state.json read/write
  llm/                 provider abstractions (anthropic, openaicompat, ollama, claude-cli)
  colour/              terminal colour helpers
  config/              flag + env resolution
```


## 8. Testing

The reference AxonOps instance for integration tests:

```
https://axonops.example.com/<org>/<cluster-type>/<cluster>/cluster-overview
```

This address should be accessed only from CI or a developer machine; set it via
`AXONOPS_BASE_URL` rather than hard-coding it in tests.

BDD feature files live under `tests/bdd/features/` using Godog. Each command
(`plan`, `apply`) has a feature file covering the happy path and at least one
invalid/edge case (unknown resource kind, missing cluster, destructive intent
without confirmation).


## 9. Decisions and open questions

Resolved:

1. **Backend default.** The default stays `terraform` and the user chooses with
   `--backend`. The aim is a reusable config in whichever tool the user runs.
2. **Multi-cluster targeting.** One cluster per request.
3. **LLM provider.** Same as ansai: `--model`/`AXAI_PROVIDER`, defaulting to
   `anthropic`, or `claude-cli` when no API key is set and `claude` is installed.
4. **Versions.** Generated projects pin the `axonops/axonops` Terraform provider
   (`~> 0.1`) and the `axonops.axonops` collection (`>=0.7.1`).

Implementation notes that differ from the sections above:

- Intake is deterministic. Org, cluster and cluster type come from flags or
  environment; a missing one is an input error. No model call is made for it.
- The fixer revises the spec rather than patching files. The revised spec goes
  through the guard and is recomposed, so generated files are never hand-edited.
- Terraform has no delete: the guard rejects `delete` for that backend. Use
  Ansible (`present: false`) or remove the resource from the Terraform project.
- Ansible output uses the `axonops.axonops.configurations` role with
  `axonops_alert_rules` set in `group_vars/all/axonops.yml`.
- Projects are additive. The cumulative spec is saved as `spec.json` in the project
  directory (default `.axai/<hash of org, cluster, type, backend>`). A request returns only
  what it adds or changes; `spec.Merge` keeps everything else, refuses a create that would
  differ from an existing resource, and applies only explicit updates and deletes.
- URLs in a request become `REQUEST_URL_<hash>` secrets before the model sees them. Re-runs
  need the value again, from the prompt or the environment.
- A request part the catalogue cannot express is listed by the model as `unsupported` and
  stops the run.
- The catalogue has `metric_alert_rule`, `custom_dashboard`, `slack_integration` and `alert_route`.
  The last three are Terraform-only.
- Only these kinds are in the catalogue so far. Adding a kind is one file
  under `internal/catalogue/`, taking its fields from the provider's
  `docs/resources` page and the collection's module argument spec.

Still open:

1. **Catalogue contributions.** Who can add template packs, AxonOps engineers
   only or community PRs?
2. **State storage.** `state.json` is local to the project directory.
3. **SaaS base URL.** The read client uses `https://dash.axonops.cloud/<org>` when
   `AXONOPS_BASE_URL` is unset; confirm this against a SaaS org.
