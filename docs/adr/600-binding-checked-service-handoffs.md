# ADR-600: Binding-checked service routing and abort handoffs

Status: Accepted

Date: 2026-10-05

## Context

ADR-598 fails closed when automatic service cutover or restoration would increase traffic without binding evidence. ADR-599 provides exact canary recovery, but a service rollout must retain its readiness, gateway acknowledgement and request-drain barriers. Operators also need to distinguish an accepted request from completed recovery.

## Decision

- At the service traffic boundary, persist a bounded binding-check request inside the existing `service_rollout_handoff` JSON. Record a request UUID, direction and exact recipient/predecessor. Under an enforced policy, an unchecked scheduler attempt commits only this request and returns `binding_release_required`; weights stay unchanged. The existing bounded JSON column needs no new migration.
- APID owns a restartable worker that reads in-flight service rollouts, checks the pinned recipient's current inventory using the stored verification age and application acknowledgement policy, and invokes the routing transaction with internal fences and the exact request UUID. It does not execute verification or smoke tasks. A single worker checks at most 32 rows per sweep, with rotating selection and bounded operation/sweep deadlines, every two seconds. Duplicate APID workers serialize and recheck the request under locks.
- Recheck request UUID/direction, predecessor retention, binding and policy revisions, evidence expiry and ready service capacity inside the routing transaction. Forward cutover requires the desired count of RUNNING service instances; restoration requires at least one ready predecessor instance. APID only reads instances. It neither boots nor drains VMs. PostgreSQL locks ready rows while publishing weights and keeps the existing traffic trigger as a backstop; MemStore mirrors the guards before mutation.
- Preserve the pinned predecessor even at zero traffic. If it disappears, the request fails closed instead of choosing another stable row. An abort replaces an outstanding forward request UUID, preventing an old worker from publishing forward routes. Scheduler progress cannot erase the stored binding receipt; late blocker updates cannot replace a successful or newer request.
- The successful routing transaction records its exact pair, internal evidence fences and audit ID, then marks the bounded check projection passed. Persist only status and at most four sanitized blockers in the handoff, never reusable grants. Subsequent scheduler ACK/drain cleanup needs no fresh binding grant when it does not increase traffic; evidence expiry after route publication must not strand cleanup.
- Extend exact `POST /v1/apps/{slug}/rollouts/recover` to active service rollouts with the same deployment/predecessor selectors. Return 202 with a `service_recovery` receipt confirming durable intent, not traffic restoration or completion. Keep successful idempotency replay. The older predecessor may be the retained zero-weight row after cutover. Existing account ownership, deploy-write, MFA and idempotency middleware remain in force. Canary recovery retains its synchronous receipt.
- Add `gregale rollouts status <slug> --deployment ID|vN [--wait]`. Resolve revisions once, pin the deployment/app on every GET, expose binding blockers and handoff progress, and finish a wait only after the selected terminal state and matching service handoff completion. Bound the wait by timeout and preserve the last status on failure; interrupt exits 130. Waiting performs no writes. Exact abort CLI receipts must match the requested pair and durable request.
- Publish additive API fields and Go, Node and Python SDK models. Scheduler readiness, gateway ACK, drain and VM lifecycle implementations remain unchanged. Completed-deployment rollback, project graph activation and positive initial canary admission still require separate checked workflows.

## Validation

Memory/PostgreSQL tests cover exact requests, wrong recipient evidence, readiness loss, binding changes, stale scheduler status, late blocker writes, forward and reverse routing and cleanup after evidence rotation. PostgreSQL lock races cover evidence expiry, policy changes, predecessor disappearance and abort intent replacing an outstanding worker. API tests cover bounded blockers, accepted-only receipts, predecessor evidence, restart recovery and preservation of scheduler barriers. CLI tests cover exact GET-only selection, routing/draining completion, timeouts and receipt mismatch. Existing scheduler barrier tests and API/SDK/schema gates are run alongside these regressions.
