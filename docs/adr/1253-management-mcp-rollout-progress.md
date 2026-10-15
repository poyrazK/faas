# ADR-1253: Bounded management MCP rollout progress

## Status

Accepted. Extends ADR-1249.

## Decision

Support standard MCP `_meta.progressToken` on `gregale_watch_rollout`. A token
is a finite protocol number or nonempty UTF-8 string of at most 128 bytes. Without
a token, the existing watch result and polling behavior remain unchanged.

After a fully validated scoped snapshot, recheck consent before sending progress
to the initiating request's session. Report the initial state and subsequent
changes using strictly increasing update numbers, with no total or completion
percentage. Suppress unchanged snapshots and cap notifications at 46 per bounded
watch. Messages contain only fixed build/handoff/binding identifiers, existing
accepted/live/healthy/completed/failed facts and bounded readiness/residency counts.
No resource IDs, gateway names, free-form diagnostics, logs or secrets are copied.

Use request-scoped SSE only for progress-enabled watches on the stateless OAuth
HTTP bridge. Other responses stay JSON. Enable HTTP cancellation propagation for
watch requests on MCP 2026-07-28 and later; legacy stateless HTTP watches remain
bounded by their watch deadline because the protocol has no retained session for
routing cancellation. Stdio cancellation uses its existing request session.

Keep notifications synchronous and request-scoped; cap the send context at 200 ms.
Notification/authorization failures stop observation rather than publishing unchecked
state. Protocol cancellation propagates through the watch to in-flight customer API
reads. No detached senders, background polling, new tools, subscriptions or mutations
are introduced. Cancellation stops observation only; it does not cancel a build,
deployment, handoff or consumer generation. Final results preserve ADR-1249's separate
artifact, health and native completion evidence; worker residency never establishes
queue processing.

## Consequences

SDK clients can display state changes before the final bounded snapshot arrives.
A fresh snapshot timestamp alone produces no progress update. Notification delivery
and client display are distinct: the pinned Codex app-server accepts call metadata,
but interfaces may not surface standard progress for direct tool calls. SDK tests
verify actual notifications, monotonicity, bounds, consent and cancellation; actual
Codex acceptance exercises calls with progress metadata over stdio and OAuth HTTP.
