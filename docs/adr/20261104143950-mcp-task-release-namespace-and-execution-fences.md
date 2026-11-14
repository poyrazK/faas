# ADR-20261104143950: MCP Task release bindings and execution deadlines

## Status

Accepted; extends ADR-20261104050000, ADR-20261104001500 and ADR-426.

## Context

Native Task releases verified the observer's namespace but could promote a
healthy web app connected to a different queue, then park the previous workers.
Node shutdown could also finish while a claim response was outstanding, leaving
an unexecuted Task to exhaust its attempt budget after lease expiry. Node's
input updates and direct runtime subscriptions did not consistently normalize
UUID aliases. Finally, all three runtimes retried failed heartbeats indefinitely
without cancelling the executor when its last proven lease expired.

## Decision

1. Native releases require the web app's explicit, non-secret
   `MCP_TASK_NAMESPACE` to match the release. Check before deployment, during
   endpoint verification, immediately before promotion and before every
   previous-worker park.
   Resuming a deployment repeats these checks; protocol readiness does not prove
   queue identity.
2. A Node drain includes an outstanding claim in its existing deadline. A claim
   returned after shutdown begins is released without starting a handler and
   resumes the same attempt. An unresolved claim at the deadline reports a
   timed-out drain rather than a completed drain. Process loss or unavailable
   database cleanup continues to use lease-expiry recovery.
3. Normalize accepted Task UUIDs to their canonical lowercase form before
   deriving input-payload AAD or building runtime subscription sets. Ownership
   checks and the original resource-subscription URI remain intact.
4. Node, Go and Python independently cancel execution when the last proven
   30-second lease budget expires, including while a heartbeat is blocked.
   A successful heartbeat renews that budget from the start of its database
   request, so response latency cannot extend local ownership. A late response
   cannot revive expired execution. Transient failures remain tolerated within
   the proven budget; PostgreSQL lease tokens and expiry checks still fence writes.

The local deadline uses a monotonic clock. A claim whose response has already
consumed that budget is released before execution. This deliberately favors
conservative cancellation when database latency makes ownership uncertain.
Cancellation remains cooperative, and handlers with external effects must remain
idempotent by Task ID. No platform quota, schema or wire format changes.

## Validation

Native CLI fixtures cover wrong, missing and sealed web namespaces, drift after
promotion and drift between worker parks. Local PostgreSQL tests cover a claim
committed during shutdown at an attempt budget of one, plus UUID input updates
and subscriptions with both payload formats. Clock-controlled tests cover
heartbeat errors, stalled and recovered requests, response latency, late renewal
and timer cleanup; Go and Python executor tests verify cancellation suppresses
completion. Live provider OAuth and native KVM acceptance remain separate gates.
