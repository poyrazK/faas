# ADR-20261026110240: Coding agents get a docs page, an AGENTS.md in every starter, and a Claude Code plugin

Date: 2026-10-26
Status: Accepted
Related: ADR-1237 (local management MCP), ADR-1238 (reviewed MCP releases), API-hosting roadmap phase 6 (agent contract)

## Context

Coding agents already have most of what they need to use Gregale: the CLI's
`--non-interactive --json` contract with categorized exit codes and Problem
errors on stderr, and `gregale mcp serve`, which exposes read tools and
reviewed release actions. An agent can't find any of it on its own:

- The automation contract lives in `docs/cli-config.md` and the management
  MCP guide in `docs/mcp.md`. Neither is published on gregale.dev, because
  both carry operator material.
- `gregale init` starters explain the first deploy to people (README.md), not
  to agents.
- Claude Code users have to wire the MCP server by hand, and get no guidance
  on a safe deploy or diagnosis.

## Decision

1. **`docs/agents.md`**, catalogued as `/docs/agents`, is a customer-only page
   covering a dedicated scoped key, the non-interactive CLI contract, MCP
   setup for Claude Code, Codex and JSON-configured clients, the plugin,
   project instructions, and the machine-readable docs that gregale.dev
   publishes (`/llms.txt`, `/llms-full.txt`, and `.md` pages, built by
   faas-web from its curated docs list).
2. **Every materialized starter gets `AGENTS.md`.** One embedded template,
   `cmd/gregale/templates/agents.md.tmpl`, is rendered with the template
   name by `templates.Materialize`, after the template's own files, so
   `init`, `start` and MCP onboarding all produce it. An existing `AGENTS.md`
   is never replaced. README.md stays the source for each template's setup
   and deploy command; AGENTS.md adds only what every project shares. Claude
   Code, Codex and Cursor read AGENTS.md natively. `gregale doctor` doesn't
   scan Markdown, so naming `FAAS_TOKEN` there raises no env finding.
3. **A Claude Code plugin lives in `packaging/claude-plugin`.** It runs
   `gregale mcp serve --transport stdio` without `--allow-writes`, and ships two
   skills: `deploy` and `diagnose`. The marketplace file is in
   `poyrazK/faas-web` and points here with a `git-subdir` source. It lives
   there because this repository is 2 GB, and `marketplace add` clones the
   repository that holds the marketplace.
4. **One test keeps it all in step with the CLI.**
   `cmd/gregale/claude_plugin_test.go` checks that the plugin's MCP arguments
   parse with `parseMCPServeOptions` and stay read-only stdio. It also checks
   that every `gregale` command and subcommand named in the page, the
   AGENTS.md template and the skills is a customer command. Finally, every
   `gregale_*` tool they name must be registered by the management server
   with writes enabled.

## Consequences

- Agents learn the contract from files they already read, with no new API,
  daemon or permission.
- The plugin uses the user's CLI login or `FAAS_TOKEN`. It holds no
  credential and makes no change unless the user adds `--allow-writes`.
- Renaming a command, subcommand or MCP tool fails the drift test until the
  agent-facing text is updated.
- AGENTS.md is part of the upload when a starter is deployed, like README.md.

## Rejected alternatives

- **An AGENTS.md copied into each of the 37 template directories:** the copies
  would drift; one rendered template can't.
- **The marketplace at this repository's root:** every install would clone
  2 GB.
- **A plugin with `--allow-writes` on by default:** release actions should be
  an explicit choice per user, as ADR-1238 requires for writes.
