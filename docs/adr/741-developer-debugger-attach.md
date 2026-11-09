# ADR-741 · Remote debugger attach for developer environments

- **Status:** proposed
- **Date:** 2026-10-08
- **Decision:** Add `gregale dev --debug`, which starts the developer workload
  with its runtime's debug listener inside the guest and exposes it only as a
  loopback port on the developer's machine. The tunnel runs over an
  authenticated, leased WebSocket to the edge, then through gatewayd-internal
  and vmmd's existing `ForwardTCPStream` RPC (ADR-183). No public TCP listener
  is created, and no production app can be debugged.
- **Why:** `gregale dev` runs code remotely, so local breakpoints are
  impossible today. Dev Bridge (ADR-378) only helps when the service is moved
  to the laptop, which loses parity with the Firecracker runtime.

## Context

- Debug protocols are remote code execution by design. Node's inspector,
  debugpy, and Delve all allow arbitrary evaluation. Exposing one is
  equivalent to granting a shell inside the VM.
- vmmd already provides a protocol-neutral, byte-capped, cancellable
  `ForwardTCPStream` from the host to a guest port (ADR-183, step 1).
- Dev Bridge established the pattern of a short-lived session with digested
  credentials, an outbound CLI WebSocket, and fencing when a connection is
  replaced (ADR-378).
- The gateway already supports HTTP Upgrade passthrough (ADR-080).

## Decision

### Runtime side

- `dev.debug` / `--debug` selects a runtime profile:
  - `node`: `NODE_OPTIONS=--inspect=0.0.0.0:9229`.
  - `python`: `python -m debugpy --listen 0.0.0.0:5678`. The developer builder
    adds `debugpy` to the developer image layer only; production images are
    never changed.
  - `go` (Delve) is deferred, because it needs a debug build and a different
    builder image.
- The debug port is fixed per runtime profile and is reachable only through
  vmmd's forward path. nftables continues to drop the port from every other
  source, and the guest's normal ingress port is unchanged.
- Only developer sessions are eligible (`preview_pr_number = 0`). apid rejects
  the flag for any other app.

### Tunnel

1. The CLI calls `POST /v1/dev/sessions/{project}/debug-attachments`. apid
   creates a row (app, workspace, random credential stored as a digest, expiry
   ≤ 1 h) and returns the credential once. Every create is audited.
2. The CLI opens `wss://…/v1/dev/debug/{attachment}` with the credential.
   gatewayd-public passes the upgrade to gatewayd-internal. That daemon
   verifies the digest, the account, the developer-session status, and the
   lease, then wakes/admits the app through schedd exactly like a request, and
   opens `ForwardTCPStream` to the debug port of the selected instance.
3. The CLI listens on `127.0.0.1:<local-port>` and accepts one debugger client
   at a time per attachment. A new attachment fences the old one.
4. While a debug stream is attached, the instance is not parked for idleness.
   Lease expiry or revocation still tears the stream down.

### Limits

`DeveloperDebugAttachments` (concurrent attachments per account),
`DeveloperDebugIdleTimeout` and a per-direction byte cap go in
`pkg/api/limits.go`.

## Consequences

- Breakpoints hold requests. The edge's request deadline and the 512-request /
  30 s wake queue still apply, so a request paused at a breakpoint will time out
  at the edge. The docs must say this, and the CLI should print the effective
  deadline.
- A paused process cannot answer readiness probes. The supervisor must not
  restart a developer instance while a debug stream is attached.
- New surfaces: an apid table, migration and endpoints; a gateway upgrade
  route; a CLI loopback listener; SDK/OpenAPI entries; metrics
  `dev_debug_attachments` and `dev_debug_stream_bytes_total`.
- With ADR-740, a restart caused by a patch drops the debugger. The CLI
  reconnects automatically within the same attachment.

## Rejected alternatives

- **Declare the debug port as a public TCP listener (ADR-183).** This would
  publish an RCE endpoint on a stable public address.
- **Carry debug traffic over Dev Bridge.** The bridge carries traffic toward
  the laptop, while debugging needs a connection from the laptop to the guest.
  The bridge is also an internal capability.
- **SSH into the guest.** There is no sshd in guests, and adding one widens
  every image.

## Rollout

1. apid attachment API, limits and audit.
2. A gateway upgrade route to `ForwardTCPStream`, the idle-park and supervisor
   exemptions, and metal tests.
3. The CLI `--debug` flag, VS Code `launch.json` examples, and docs.

## Implementation (2026-10-09)

Implemented for Node.js; metal tests remain to be run on the native hosts.
It differs from the decision above in these places:

- **No attachment table.** The tunnel is a compute-owned API path,
  `GET /v1/apps/{slug}/debug` (WebSocket), served by gatewayd-internal beside
  the log stream. It reuses the existing API-key middleware: deploy-write
  scope, MFA gate, rate limit, and the IDOR-safe app lookup. A short-lived
  attachment credential added no protection over the caller's own key, so
  steps 1–2 collapse into this route. gatewayd-public routes the path to the
  compute gateway like `/logs`.
- **Developer apps only, enforced at the route.** The handler refuses any
  app that is not a `gregale dev` environment (409), refuses browser
  `Origin`s, and admits at most `DevDebugSessionsPerApp` concurrent sessions
  (429 `dev_debug_session_limit`). Sessions use `DevDebugIdleTimeout` and a
  `DevDebugMaxBytes` cap per direction (`pkg/api/limits.go`).
- **Instance selection** goes through the service proxy's endpoint registry
  and wake path (`ServiceProxy.WakeTarget`), so a parked developer app wakes
  for a debugger like it does for an internal service call.
- **Enabling the inspector** is a value on the developer app, not a builder
  change: the CLI sets the `FAAS_DEV_DEBUG=node` secret (and removes it when
  started without `--debug`). guest-init appends
  `--inspect=0.0.0.0:9229` to `NODE_OPTIONS` for the main workload. A Python
  profile (debugpy) is deferred because the image does not ship debugpy.
- **Park and liveness.** An open `ForwardTCPStream` counts as in-flight work,
  which the idle reaper and pressure parking already respect, so no new
  park exemption was needed. While debugging, guest-init answers liveness
  probes as healthy so a process paused at a breakpoint is not destroyed.
- **The CLI** listens on `127.0.0.1:9229` (`--debug-port`) and opens one
  tunnel per debugger connection, so a debugger that reconnects after a
  live-patch restart (ADR-740) gets a fresh session.

## Amendment (2026-11-09): hardening after the debugger review

A review of the attach path found that the inspector could be switched on
outside developer environments, that liveness stayed forced healthy whenever
`--debug` was on (attached or not), that the preload opened the inspector in
the package-manager or watcher wrapper instead of the server, and that
functions killed or replaced a worker held at a breakpoint.

- **Developer marker.** schedd stamps `FAAS_DEVELOPER_ENVIRONMENT=1` on
  developer apps as platform identity (authoritative over customer env and
  secrets). guest-init honours `FAAS_DEV_DEBUG` only with that marker, so a
  production app that sets the variable gets a logged warning and no
  inspector.
- **Liveness exemption only while attached.** guest-init forces liveness
  healthy only while an inspector connection is established on port 9229
  (read from `/proc/net/tcp{,6}`), and only when the inspector it opened (or
  the app's own `--inspect` on a wildcard address) is the one listening.
- **Preload targets the server.** The preload skips npm, yarn, pnpm,
  corepack, nodemon, tsx, ts-node-dev, concurrently, npm-run-all, Next's CLI,
  `.bin` shims and `node --watch` parents.
- **Functions.** While debugging, the runner pool keeps one worker, and a
  timed-out or cancelled invocation on a worker the debugger holds waits for
  the worker instead of retiring it.
- **Tunnel.** The gateway emits `dev.debugger.attached` and
  `dev.debugger.detached` audit events (app, instance, key, duration), and
  picks one instance deterministically (lowest instance id) when several are
  live. The CLI waits for the full wake timeout plus a margin, caches the
  `/json` discovery requests for two seconds, refuses Bun and Deno projects,
  and reads `dev.debug_port` from `gregale.yaml` so `--all` can debug several
  apps on distinct ports.
- **Documented limits.** The per-app session cap is counted per
  gatewayd-internal process, not cluster-wide; with N gateway processes an
  app can hold up to N × `DevDebugSessionsPerApp` tunnels. The cap bounds
  resource use per process, which is what it protects, so it stays local.
  The idle and byte caps close a session; the debugger reattaches. The tunnel
  route `GET /v1/apps/{slug}/debug` is served by gatewayd, not apid, and is
  documented in `docs/gregale-dev.md` rather than the apid OpenAPI spec,
  whose route parity and SDK generation cover apid only.
- **Test gap.** The metal test drives `FAAS_DEV_DEBUG` through the developer
  env path only. The developer marker itself is platform identity and is
  covered by a schedd unit test; a metal case for a production app that sets
  `FAAS_DEV_DEBUG` (expect no inspector) is still to be added.
