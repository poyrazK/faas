# ADR-20261026152009: Management MCP toolsets and a coding-agent eval

Date: 2026-10-26
Status: Accepted
Related: ADR-1237 (local management MCP), ADR-20261026110240 (agent docs and plugin), ADR-20261026141841 (local deploy and env tools), API-hosting roadmap phase 6

## Context

The management MCP server lists every tool to every connection. Measured
through `gregale mcp serve` on 2026-10-26, with the local deploy and env tools:

- 51 read tools: 51 KB, about 13,000 tokens;
- 62 tools with `--allow-writes`: 58 KB, about 14,500 tokens.

A client loads each listed tool's schema into the model's context on every
turn. Most of the catalog is deep diagnostics: wake timelines, CPU profiles,
critical paths. Few sessions need them, yet every session pays for them. A
large catalog also makes tool choice less reliable.

Nothing measured whether agents succeed with Gregale at all. Roadmap phase 6's
exit gate asks for "an authorized coding agent" to complete the same scenario
as the CLI. There was no way to run that scenario, or to tell whether a change
to tools or docs helped.

## Decision

1. **Toolsets.** `gregale mcp serve --tools core|release|diagnose|all` takes a
   comma-separated list and defaults to `all`, so existing configurations keep
   their behavior. It applies to stdio and to every remote grant.
   - `core` (18 tools): apps, app deployments, logs, metrics, health, error
     groups, local deploy and env changes, promote and rollback. That is
     11.5 KB against 54.9 KB for `all` (encoded tool list).
   - `release` (21 tools): image and Git source releases, service and worker
     rollouts, named environments, route budgets, preflight, account
     deployment history.
   - `diagnose` (23 tools): request, analytics, profile, wake, trace,
     regression and incident diagnostics, including regression triage writes.

   Write tools still need `--allow-writes`. Resources and prompts are always
   listed. They cost nothing per turn, and the review prompts point at
   resources, not tools.
2. **Every tool belongs to exactly one toolset.** One map, `toolsetOf`, holds
   the assignment. A registered tool missing from it is never listed under a
   toolset. `TestEveryToolHasAToolset` fails on an unassigned or stale name,
   and `TestCoreCatalogStaysSmall` holds `core` to 16 KB.
3. **The verbose core descriptions are shortened** (error groups, wait, local
   deploy, env change) without dropping a constraint the agent needs.
4. **The Claude Code plugin and the agents page use `--tools core`.** The
   plugin's version becomes 0.2.0.
5. **`scripts/agent-eval`** runs Claude Code and Codex headless against
   Gregale tasks:
   - Four tasks: deploy, fix a failed deploy, roll back a bad release,
     diagnose a failure.
   - Each runs at a chosen toolset, or `none` for CLI only.
   - Checks: the app's HTTP response, the serving release, untouched files,
     and the agent's answer.
   - It records success, turns, tool calls, tokens, cost and time per run.
   - Runs need `GREGALE_EVAL_TOKEN`, a dedicated evaluation account's API key.
     The harness strips the caller's `FAAS_*` variables and deletes each run's
     app.
   - `make agent-eval-test` (in ci-light) tests the parsers, commands and
     checks against recorded transcript shapes without starting an agent.

## Consequences

- An agent configured with `core` loads about a fifth of today's catalog. It
  adds `diagnose` or `release` when the work calls for them.
- A new tool must be assigned a toolset, or the build fails.
- Changes to tools, descriptions, docs and the plugin can be compared by
  success rate and cost, not by intuition. The first baseline needs an
  evaluation account and a budget, and is not part of this change.
- The harness costs real money per run: a trivial Claude session costs about
  $0.06 on Sonnet. Budgets are per task.

## Rejected alternatives

- **Changing the default to `core`:** it would silently remove tools from
  existing configurations. The plugin and the docs opt in instead.
- **A per-tool allowlist flag:** it is too fine-grained for a user to choose
  well, and impossible to keep stable as tools are added.
- **One meta-tool that dispatches to the others:** it hides schemas from the
  model, which then guesses arguments. It also defeats MCP annotations
  (`readOnlyHint`) that clients use for permissions.
- **Running the eval in CI on every change:** each run deploys apps and
  spends model budget. It runs on demand with an evaluation account; only its
  unit tests run in CI.
