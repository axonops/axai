# axai - CLAUDE.md

## Project Overview

Go module/application repository scaffolded from `axai`.

## Current Status
- **Last Updated**: TBD
- **Current Phase**: Development
- **Health**: Green

## Global standards — see ~/.claude/DIGITALIS.md

Shared engineering standards live in `~/.claude/DIGITALIS.md` (installed from the `claude-skills` marketplace) and are **not** repeated here: terse communication, KnowledgeRelay/RAG sourcing for AxonOps/Digitalis/customer questions, secrets management, signed + DCO commits with brand-based identity, branding, README requirements, and the NEVER-DO list. This file covers only Go-specific workflow and tooling.

## Active Tasks
None.

## Recent Progress
- Initial scaffold from `axai`

## Blockers & Issues
None currently.

## Architecture & Key Decisions
- **Language**: Go 1.23+
- **Lint**: `golangci-lint` (config in `.golangci.yml`)
- **Pre-commit**: `gofmt`, `go vet`, `go mod tidy`, `golangci-lint`
- **Build**: standard `go build`, orchestrated via `Makefile`

## Useful Commands

```bash
make help          # Show all targets
make build         # Compile
make test          # Run unit tests
make test-race     # Run with race detector
make cover         # Coverage report
make lint          # golangci-lint
make fmt           # gofmt -s -w .
make vet           # go vet
make tidy          # go mod tidy
```

## Required Claude Code plugins

Agents and slash commands live in the AxonOps shared marketplace at
`bitbucket.org/digitalisio/claude-skills`. Install once per developer using Claude Code's native plugin marketplace:

```text
/plugin marketplace add git@bitbucket.org:digitalisio/claude-skills.git
/plugin install engineering-agents@axonops-claude-skills
/plugin install go-bootstrap@axonops-claude-skills
/plugin install kubernetes@axonops-claude-skills          # OPTIONAL: install if the binary talks to the k8s API or ships a Helm chart
/plugin install kafka@axonops-claude-skills               # OPTIONAL: install if the binary produces/consumes Kafka (kafka-go, sarama, confluent-kafka-go)
/plugin install cassandra@axonops-claude-skills           # OPTIONAL: install if the binary talks to Cassandra (gocql, gocqlx)
```

### Keeping skills current

Plugins evolve. Pull the latest catalog and update everything in one go:

```text
/plugin marketplace update axonops-claude-skills
/plugin update
```

Optional opt-in: a SessionStart hook can warn when this marketplace is behind. Paste into your personal `~/.claude/settings.json`:

```json
{
  "hooks": {
    "SessionStart": [{
      "matcher": "startup",
      "hooks": [{
        "type": "command",
        "command": "test -d ~/.claude/plugins/marketplaces/axonops-claude-skills && git -C ~/.claude/plugins/marketplaces/axonops-claude-skills fetch --quiet 2>/dev/null && [ \"$(git -C ~/.claude/plugins/marketplaces/axonops-claude-skills rev-list HEAD..origin/main --count 2>/dev/null || echo 0)\" -gt 0 ] && echo 'axonops-claude-skills: updates available — run /plugin marketplace update axonops-claude-skills && /plugin update'"
      }]
    }]
  }
}
```

The legacy `git clone … install.sh --plugin <name>` flow is still supported for non-Claude-Code consumers but is deprecated — it cannot track versions or report updates.

Plugins used:
- `engineering-agents` — `secrets-auditor`, `docs-quality-reviewer`, `code-reviewer`, `go-quality`, `go-bug-fixer`, `test-writer`, `kube`, `tech-decision-maker`
- `go-bootstrap` — `go-specialist`, `issue-writer` (Go-specific), `godog-bdd-tests` skill, `/create-pr`, `/create-jira-ticket`, `/create-github-issue`
- BDD skills: `engineering-agents:bdd-guidelines` (generic rules — load alongside the stack BDD skill) and `go-bootstrap:godog-bdd-tests` (Godog realisation)

## Workflow — Agent Gates

These agents are mandatory gates, not optional tools.

### When starting any new feature or significant change:
- **`engineering-agents:bdd-guidelines`** + **`go-bootstrap:godog-bdd-tests`** — write Gherkin `.feature` files under `tests/bdd/features/` describing public-API behaviour (happy path + at least one invalid/edge case) **before** implementation. Step defs are black-box (`package bdd_test`). Cleanup via `sc.After` hook, registered immediately after each side effect.
- **`agent-skills:test-driven-development`** — failing unit/BDD test first, then make it pass.

### Before any commit:
- **secrets-auditor** — verify no plaintext credentials, API keys, or tokens

### Before opening a PR:
- **go-quality** — idiomatic Go, lint compliance, anti-patterns
- **docs-quality-reviewer** — README quality and branding compliance
- **code-reviewer** — bugs, regressions, missing tests
- **security-reviewer** — input validation, OWASP, dependency review
- **test-writer** — coverage gaps, missing edge cases
- `CHANGELOG.md` must be updated (Keep a Changelog format) with every change in the PR

### Before creating a Jira issue:
- **issue-writer** — full requirements, acceptance criteria, testing notes
- Then **atlassian:triage-issue** for duplicate detection

## Code Standards

- All exported identifiers must have doc comments starting with the identifier name
- Errors wrapped with `fmt.Errorf("...: %w", err)` — never lose error chain
- Context-aware functions accept `ctx context.Context` as first arg
- No `panic` in library code — return errors
- Tests live next to source (`foo_test.go`)
- Race detector must pass (`make test-race`)
- Coverage target: ≥ 80% for non-trivial packages

## Known Limitations & Tech Debt
- Document here as discovered

## Next Steps & Roadmap
- Define module structure
- Add CI matrix (multiple Go versions if needed)
