# Changelog

All notable changes to this project are documented in this file.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

- `axai plan` without `AXONOPS_API_TOKEN` warns, skips the cluster read and the Terraform/Ansible plan, and only writes the project. `axai apply` still requires the token.
- `axai plan`, `axai apply` (alias `run`), `axai status` and `axai doctor`.
- Terraform and Ansible project generation for AxonOps metric alert rules.
- Deterministic guard that validates the model's spec against the catalogue.
- Read-only AxonOps API client that gives the model real dashboard and chart names.
- Bounded fixer loop (`--max-fixes`) that revises the spec after a failed apply.
- Model providers: Anthropic, OpenAI, Gemini, Ollama, llama.cpp, OpenAI-compatible and the Claude Code CLI.
- Catalogue kinds `metric_alert_rule`, `custom_dashboard` (counter, line and pie panels), `slack_integration` and `alert_route`.
- Additive projects: each request is merged into the project's `spec.json`. Existing resources are kept, a create that would change one is refused, and only an explicit update or delete changes it.
- URLs in a request are treated as secrets: the model and the saved project see only a `REQUEST_URL_<hash>` placeholder.
- A request part the catalogue cannot express stops the run instead of being dropped.
- Godog feature files for `plan`, `apply` and additive projects.
