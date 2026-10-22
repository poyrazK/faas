# ADR 1061: Python async realtime worker helper

Status: Accepted; runtime validation deferred.

## Context

Python applications can use the generated realtime API, but each application would otherwise implement claim polling, lease monitoring, concurrency and shutdown. The Node SDK already provides this lifecycle.

## Decision

Add `consume_realtime_consumer_group` for account authentication and `consume_realtime_consumer_group_with_credential` for group-bound worker credentials. Both use the caller-owned generated `AuthenticatedClient` async transport and existing API functions. Read status once, freeze that group UUID/lease policy, and fill only available handler slots with bounded batch claims. Defaults are concurrency one, batch size one, long poll ten seconds and one-second empty polling. Bounds mirror the existing server and Node limits. Recreated groups or malformed renewal responses stop the worker.

Provide an async handler with message and `RealtimeWorkerContext`: its cooperative stop event and the original claim, including group UUID/fencing token for downstream stale-write rejection. Claim tokens are secrets and excluded from context repr; applications must not log/cache the underlying claim. Handlers must await all effects and respond cooperatively to stop. They must not block the event loop. Neither handler cancellation nor a returned message is proof of exactly-once external processing.

Renew each active lease at one third of its configured duration. Independently watch the observed lease expiry and delivery deadline, signal handlers on loss/expiry and stop new claims. Renewal results refresh the local expiry. Completion follows successful handler return; a handler exception requests retry with a safe diagnostic code. Default explicit retry delay is one second; opt in to the group's retry policy by omitting delay, or supply an explicit delay. Notify the optional handler-error callback only after successful retry/dead settlement. Infrastructure/auth/renewal/settlement errors stop the worker and propagate without copying HTTP response bodies into errors. HTTP claim throttling is retried only for known rate/circuit/dedup admission codes with positive hints; claims and uncertain settlements are never automatically retried. Partial retention/capacity blockers drain returned claims before surfacing recovery requirements.

Optional worker readiness heartbeats run initially and every fifteen seconds. Scoped clients need status, claim, renew, complete and retry grants, plus heartbeat/release when those options are enabled. Issued credential identity is fixed; account workers specify their identity. The caller owns HTTP timeouts, transport and client closure. Use a timeout longer than the long-poll duration and disable custom automatic retries for mutation endpoints.

A supplied asyncio.Event requests cooperative shutdown. Cancelling the worker task does the same cleanup, then propagates cancellation. Stop claim requests, notify handlers, await them, and stop/join renewal/watch tasks before releasing any lease. Release is opt-in, caller-shutdown only, requires a still-valid lease, and has an independent five-second request budget. Internal failures/known lease loss never release work, and an uncertain terminal settlement is never followed by release. Release failures propagate. Repeated cancellation does not orphan owned worker tasks; a handler that ignores its stop event can delay shutdown indefinitely. Cancellation during an in-flight claim can leave an unknown server lease to expire.

Keep the helper handwritten and export its entry points/options/context/errors from the package barrel. Extend SDK generation preservation and the authoritative barrel template so regeneration retains the module and public exports. No new backend routes, worker permissions, OpenAPI changes or migration.

## Consequences

Python services can supply application handlers while the SDK manages lifecycle. Settlement batching and receipt-backed automatic retries remain available through low-level APIs, with helper integration deferred. Python syntax/lint, generated-interface checks and SDK regeneration are allowed verification for this change. Runtime tests, exercising servers and migrations remain deferred at the user's request.
