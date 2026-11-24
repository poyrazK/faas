# ADR-20261123003000: Hold MCP notification streams at the gateway while the app parks

- Status: Proposed (design only; no code until the open questions are decided
  and metal evidence exists)
- Date: 2026-11-23
- Related: ADR-20261122194324 (section 5), ADR-156 (managed realtime),
  ADR-1032 (scoped realtime publisher credentials), durable MCP Tasks
  (`input_required` recovery, #7861)

## Context

Hosted MCP servers are stateless, and durable Tasks keep task state in
Postgres. A tool that needs a person's answer returns `input_required`; the
client polls `tasks/get`, and between polls the app can park. That already
covers approval and elicitation waits of minutes or hours without a running VM.

Notification streams are different. `subscriptions/listen` (protocol
2026-07-28) is a long-lived SSE response that the starter holds open inside the
guest (`mcp-go-tasks/subscriptions.go` clears the request deadline while idle).
An open request keeps the instance busy, so an app with one listening client
never parks and is billed for every second the client listens. Clients that
want `notifications/tools/list_changed`, `notifications/resources/updated` or
task status changes therefore keep the VM running.

## Decision (proposed)

1. **The gateway answers `subscriptions/listen`** for MCP apps whose stored
   policy opts in (`mcp.hold_streams: true`). It authorizes the request with the
   existing MCP policy (including each resource subscription against
   `resource_scopes`), sends the acknowledgement, and holds the SSE response
   itself with keepalive comments. The request never reaches the guest and
   never wakes it.
2. **The app publishes notifications** to one realtime channel per app through
   a scoped publisher credential (ADR-1032), instead of writing to open
   streams. Starters gain a `publishNotification(kind, uri?)` helper; the
   catalog change and resource update paths call it.
3. **The gateway fans out** each published notification to the held streams
   whose filters match, using the managed realtime subscription it already has
   on the node. Notifications carry no payload beyond what the protocol
   defines (URIs, task IDs), so the gateway never holds tool output.
4. **Task status** changes publish `notifications/tasks/status`, so a client
   waiting on `input_required` can listen instead of polling. The VM parks
   between events.
5. **Limits** in `pkg/api/limits.go`: held streams per app and per account by
   plan, stream lifetime, and keepalive interval. Over the limit, the request
   falls through to the guest as today.

## Open questions (owner decisions before any code)

- **Billing.** Held streams cost gateway memory and realtime fan-out, not VM
  time. Unbilled, a per-stream-hour price, or counted against realtime
  connections?
- **Delivery.** Realtime is at-least-once with replay by message ID; MCP
  streams resume with `Last-Event-ID`. Map one to the other, or drop
  resumption and document that clients re-list after a reconnect?
- **Multi-node.** Each `gatewayd-public` holds its own streams; fan-out relies
  on every node's realtime subscription. Is per-node subscription load
  acceptable at the stream caps above?
- **Ownership.** This makes the gateway answer an MCP method, which today only
  the guest does (§6 component ownership). It needs the same scrutiny as the
  gateway MCP policy: no tool execution, no catalog reads, only the listen
  method.

## Evidence required before release

- Metal: an app with N held streams parks (cgroup gone, invariant 4) and wakes
  only for tool calls; `make leakcheck` clean.
- A published `tools/list_changed` reaches every matching stream on every node
  within the realtime p95.
- A revoked consumer key or expired token closes its held stream within the
  policy convergence window (ADR-1242).

## Rejected

- **Keeping streams in the guest across snapshots.** A parked app has no
  process to hold a socket; restoring on every keepalive defeats parking.
- **Polling only.** Works today for Tasks, but notifications such as
  `list_changed` have no pollable source and clients would re-list blindly.
