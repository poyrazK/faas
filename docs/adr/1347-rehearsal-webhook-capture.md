# ADR-1347 · Durable rehearsal webhook capture

- **Status:** accepted
- **Date:** 2026-10-16
- **Decision:** Add opt-in managed-webhook capture to scenario registration. Bind an immutable capture policy to each fresh developer-session app before deployment, retain it beyond namespace cleanup, and capture managed operation/workflow effects inside their existing completion transactions. Fence all associated webhook outbox events at both the database and dispatcher boundaries.
- **Why:** Source isolation and application-reported comparisons do not stop a webhook dispatcher from contacting a real receiver. Guarding only the `operation.effect` event would leave ordinary events and manual retries able to escape.
- **Consequences:** Captured effects retain their existing identity, receiver, event type and bounded payload, plus the rehearsal run ID. They have status `captured`, no delivery ID, and no queued delivery. Normal receiver/tenant/account checks and ownership fencing remain mandatory. Namespace deletion and expiry never restore live dispatch authority.
- **Rejected alternatives:** Redirecting a receiver URL can affect other work and cannot fence an already queued retry. Trusting a request header lets application code select authority. An in-memory flag is lost on restart. Treating attempted deliveries as distinct effects confuses at-least-once transport with duplicate business intent.

## Registration and lifecycle

`actions: {webhooks: capture}` is accepted only by the real-VM scenario engine.
Registration passes `webhook_mode: capture` to the existing account-authenticated
scenario endpoint. It still verifies exact developer-session/run ownership.
Capture registration refuses apps with deployments, managed operations, workflows,
or webhook deliveries. Registration of all members and policies is atomic; app
locks serialize it with outbox insertion. The server returns a version 1
acknowledgement only after committing. The CLI refuses to deploy source without
that acknowledgement, including when talking to a server returning legacy 204.

The policy is independent of `scenario_test_members`. Pruning or deleting the
service namespace cannot remove it. App expiry or missing membership rejects
new managed effect completion; delivery remains blocked. Physical app/account
purge removes its policy through existing ownership cascades, alongside its
associated outbox rows. Routine migration rollback refuses to remove capture
authority while enrolled apps remain.

## Completion and delivery

Both exclusive operation completion and managed workflow step completion check
the retained policy after their normal scope and receiver checks. A captured
effect and business result are committed together in Gregale; an invalid later
effect rolls back the entire completion. No webhook delivery is enqueued.
Repeated/stale completions keep their existing fencing, and differently named
effects retain multiplicity. Existing SDK transaction receipt behavior remains
necessary: Gregale's transaction does not include the application's database.

Inspection through operation status or workflow attempts reports `captured` and
`rehearsal_run_id`. Capture identity remains available if a receiver is later
disabled or removed. This records the receiver identity validated at completion;
it does not attest a snapshot of all mutable receiver configuration. The
subsequent [sealed comparison decision](1348-sealed-rehearsal-action-comparison.md)
adds a configuration fingerprint at completion and a retained run inventory.

Unsupported ordinary webhook events for an enrolled app or its app receiver are
stored as `dead` with `rehearsal: unsupported webhook delivery suppressed`.
A database trigger also fences direct/older writer paths and manual retries.
Every dispatcher attempt additionally checks retained capture authority before
loading secrets or contacting a receiver. Policy read errors leave the claim
untransmitted for lease recovery. Existing ordinary production outbox behavior
and operation-effect destination checks remain unchanged outside enrolled apps.
Older completion workers can produce suppressed ordinary ledger rows instead of
the new captured-effect status; full capture inspection requires upgraded workers.

## Supported evidence and qualification

Portable tests exercise both memory and PostgreSQL completion/rollback, stale
completion, effect multiplicity, expiry, ordinary-event suppression, retry,
namespace cleanup, enrollment/enqueue races, dispatcher HTTP suppression, and
missing acknowledgement. API/SDK contracts expose the new status and handshake.
Existing non-rehearsal operation/workflow/webhook tests remain regression gates.

This increment is the durable policy and managed-webhook adapter. CLI policy
receipts indicate an acknowledged configuration, not automatic behavior coverage.
Connecting stored intent to the two-revision comparison is specified by ADR-1348;
the behavior observer contract remains explicitly application-reported. Direct
application HTTP/SMTP calls, other integrations, workload egress containment,
production data cloning, and native hosted qualification are outside this change.
