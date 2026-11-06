# axai

Turn a natural-language request into reusable AxonOps configuration, as a Terraform or Ansible project.

```bash
export AXONOPS_API_TOKEN=...            # never written to generated files
export AXONOPS_ORG=mycompany AXONOPS_CLUSTER=prod AXONOPS_CLUSTER_TYPE=cassandra
export AXONOPS_BASE_URL=https://axonops.example.com   # self-hosted only
export AXONOPS_TOKEN_TYPE=AxonApi                      # self-hosted only
export ANTHROPIC_API_KEY=...

axai plan  "Create an alert when disk usage is over 80%"
axai apply "Create an alert when disk usage is over 80%" --yes --git
axai apply "Create an alert when disk usage is over 80%" --backend ansible
```

The project is written to `.axai/<hash>` (or `--project`) and can be re-run without axai or a model.

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--org` | `AXONOPS_ORG` | AxonOps organisation |
| `--cluster` | `AXONOPS_CLUSTER` | Cluster name |
| `--cluster-type` | `AXONOPS_CLUSTER_TYPE` | `cassandra`, `kafka` or `dse` |
| `--backend` | `terraform` | `terraform` or `ansible` |
| `--project` | `.axai/<hash>` | Project directory |
| `--model` | `AXAI_PROVIDER` | `anthropic`, `openai`, `gemini`, `ollama`, `llamacpp`, `openai-compatible`, `claude-cli` |
| `--yes` | false | Skip confirmation; deletes still ask |
| `--max-fixes` | 3 | Fixer attempts after a failed apply |
| `--git` | false | Commit the project after a successful apply |

Exit codes: 0 success or no changes, 1 input, 2 validation, 3 apply failed, 4 runtime.

See [docs/DESIGN.md](docs/DESIGN.md) for the design.

---

Maintained by [AxonOps](https://axonops.com). Support at [axonops.com/contact](https://axonops.com/contact).
