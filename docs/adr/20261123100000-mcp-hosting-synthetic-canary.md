# ADR-20261123100000 · Synthetic canary for MCP hosting

- **Status:** accepted for implementation; activation needs the `MCP_CANARY_KEY` secret.
- **Date:** 2026-11-23
- **Extends:** [synthetic canary missed-run monitor](20261113100000-synthetic-canary-missed-run-monitor.md),
  [MCP hosting prod probe fixes](20261121160000-mcp-hosting-prod-probe-fixes.md).

## Context

MCP hosting is a launch headline. The production deploy canary covers
deploy-and-invoke for plain apps every six hours, but nothing watches the MCP
path. Three days of manual prod probes found failures that no existing alert
would see: the default Node starter never promoted, hosted `get_logs` always
returned 404, starters cut responses at 15 s, the first outbound call after a
wake hung, and apps could not reach `*.gregale.app` URLs served by the other
compute node. Each was found by a person, hours or days after it shipped.

The deploy canary also depends on control-plane SSH from a fleet runner. That
path has been broken for days at a time (stale `CP_HOST`), which silenced it.

## Decision

Add `.github/workflows/mcp-canary.yml` and `scripts/ops/mcp_canary.py`.

- **Outside-in.** It runs on a GitHub-hosted runner, installs `gregale@latest`
  from npm and uses only public endpoints. No SSH and no fleet runner, so it
  fails independently of the things it watches.
- **Hourly checks against a persistent app** (`mcp-canary`): 2026-07-28
  discovery, 2025-06-18 negotiation, catalog, a tool call, SSE progress, a
  20 s silent call, external egress, app-to-app through every entry IP, park
  and wake within a 10 s budget, the first outbound call after a wake, and the
  hosted management MCP (OAuth metadata, `tools/list`, `gregale_get_logs`).
- **Deploys every sixth hour.** A fresh Go starter from the released CLI plus
  `deploy/canary/mcp/_starter/canary_tools.go`, added through the starter's public
  `RegisterMCPTool` hook so the canary runs the starter customers receive.
  The app is also deployed whenever it does not exist.
- **Flaky is not failed.** Each check except park/wake retries once. A pass on
  retry is a workflow warning; only a second failure fails the run.
- **Alerts reuse the deploy canary's paths.** Scheduled failures post to
  `CANARY_FALLBACK_WEBHOOK_URL`; scheduled successes ping
  `MCP_CANARY_HEARTBEAT_URL` through `canary_heartbeat.py`, so a disabled
  schedule or lost runner is caught by the external deadline monitor.
- **One credential.** `MCP_CANARY_KEY` is an org agent key with scopes
  `agent,deploy:write` and approvals `none`, limited to the canary project. It
  is the CLI's `FAAS_TOKEN` and the hosted MCP bearer, and is never logged.

## Consequences

- The canary costs one micro app (scale to zero, woken hourly) and four builds
  a day.
- It does not cover the external-OAuth starter or Node/Python starters; a
  real identity provider and per-runtime apps are follow-ups.
- Paging is inert until the owner sets `MCP_CANARY_KEY`,
  `CANARY_FALLBACK_WEBHOOK_URL` and `MCP_CANARY_HEARTBEAT_URL`.
