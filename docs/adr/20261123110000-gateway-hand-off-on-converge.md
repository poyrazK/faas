# ADR-20261123110000 · Gateways hand off on converge

- **Status:** accepted for implementation; merge after the 2026-11-23 launch.
- **Date:** 2026-11-23
- **Extends:** [ADR-1736 zero-downtime daemon restarts](20261018072701-zero-downtime-daemon-restarts.md).

## Context

ADR-1736 lets gatewayd-public, gatewayd-internal and realtimed hand off on
`systemctl reload`: a successor starts on the same sockets and the old
generation finishes its requests and streams. Only `deployctl
restart-changed` (the per-unit lane) uses it.

Full releases converge with Ansible. The gateway roles' handlers ran
`systemctl try-restart` (realtimed: `state: restarted`), so every full release
stopped the gateways first: a 25 s drain that ends SSE and WebSocket streams at
once and cuts any request still running. On 2026-11-23 the MCP prod probe lost
20–200 s tool calls (503 and HTTP/2 resets) whenever bhs-3's edge restarted;
MCP tool calls routinely run minutes.

## Decision

The gateway handlers run `/usr/local/sbin/faas-reload-or-restart UNIT DAEMON`
(installed from `roles/_shared/files` by each gateway role):

- Hand off when the unit is active, systemd has no pending unit reload, its
  `ExecReload` sends `USR2`, no unit file, drop-in, environment file or
  credential source changed since the unit started, and both the running and
  the new binary answer `-handoff-capable`. These are deployctl's
  `handoffBlocker` rules; keep the two in step.
- Otherwise restart, as before. A hand-off cannot apply a changed unit or
  environment, because the successor inherits its predecessor's environment.
- Wait up to 210 s (memory wait + ready timeout + slack) for the successor to
  become the main process. A failed or stalled hand-off falls back to a
  restart. A successor rejected by its pre-traffic check fails the handler
  and is not restarted, so a bad binary never replaces a serving one.
- realtimed keeps `state: restarted`'s start-when-stopped behaviour.

## Consequences

- Binary-only releases no longer cut gateway streams or long requests.
  Releases that change a gateway unit, drop-in or env file still restart it.
- vmmd and schedd are still restarted by converges; whether that interrupts
  proxied requests is a separate question.
- Test: `faas-reload-or-restart_test.sh` (fake systemctl, ten cases) runs in
  CI; a metal converge with a long call held open across it is the
  acceptance check before merge.
