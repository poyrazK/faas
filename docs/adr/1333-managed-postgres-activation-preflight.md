# ADR-1333: durable managed PostgreSQL activation preflight

Status: accepted for catalog inspection; customer activation disabled.

## Context

ADRs 464–467 stage and verify target credentials and fence admission. ADRs 993
and 1153 retain native retirement and instance catalog evidence. None establishes
that all database writers stopped or authorizes publishing restored bindings.

## Decision

Add an account-scoped preflight operation to the existing cutover. POST
`/v1/postgres/cutovers/{id}/preflight` records `pending`; GET reads progress.
The existing cutover recovery worker evaluates pending requests after restart,
including with provisioning disabled. The operation makes no provider or VM
calls and never installs fences, changes bindings, publishes secrets or resumes
workloads. Requests against cancelling/cancelled cutovers conflict.

Concurrent pending retries preserve the revision. An explicit request after a
completed check increments it and erases previous observations. Evaluation
records `blocked`, the affected source binding IDs, exact source/target placement
and generation matches, restore lineage, absence of target bindings, complete
source binding membership/generations/access, and freshness of every staged
credential's existing SQL verification. PostgreSQL reads and writes use one
repeatable-read transaction serialized against cutover changes. A failed or
interrupted transaction leaves the request pending for recovery.

Checks are observations with a cutover timestamp, check time and expiry bounded
by the earliest credential verification expiry and five minutes. GET recomputes
staleness for expiry, future clocks, cutover changes and cancellation. Positive
fields describe the recorded observation even after expiry; clients must inspect
`stale`. HTTP idempotency replay returns the original response; GET obtains latest
progress. Every response disables caching and `activation_allowed` is always false.
Sealed envelopes, provider identifiers and future target binding IDs stay private.

## Runtime drain observations

Preflight also records a safe `runtime_drain` summary of the existing ADR-1153
plan: inventory count, pending/retired/blocked counts, plan blockers, and each
captured instance's deployment, scope, mode, state and blocker. No plan is
reserved by inspection. Missing admission fences and missing plans have distinct
findings. The in-memory adapter has no scheduler ledger and reports `unavailable`.

The scheduler's receipt evaluator is shared with a read-only snapshot observer.
Evaluation and GET use repeatable-read transactions for authority, plan membership,
current incarnations and native receipts. The observer takes no app locks; this
avoids reversing the app-before-cutover mutation lock order. Scheduler reservation
and retirement paths retain their existing locks and ownership. A terminal instance
without its matching native receipt remains pending. Unsupported members and
incomplete membership remain visible blockers.

The durable report stores only the public summary plus a private SHA-256 digest
covering current app owner/revision/status, admission fence, plan frame and receipt
identities. GET re-observes runtime evidence and marks the historical report stale
when that digest differs. Ownership ABA, changed fences, new plans, receipt progress
and invalid receipts invalidate earlier observations. Confirmed instance retention
preserves the original plan member and receipt. No runtime state is rewritten by
GET; an explicit new preflight revision obtains fresh findings. Completed reports
created before runtime observations were supported are stale until retried.

Host, wake, kernel, native generation, plan IDs and the digest remain private.
`catalog_retired` is only the captured instance catalog verdict; it never removes
`runtime_safety_unproven`, `source_sessions_unfenced` or `activation_disabled`.

## Boundaries and follow-up

The operation always reports the missing activation gate, runtime safety,
source-session fencing, route convergence, snapshot invalidation and explicit
resume plan. It does not interpret an empty catalog, terminal state, or even
`catalog_retired` as proof of writer isolation. Scheduler and vmmd ownership is
unchanged. Provider restore correctness and writes after the restore point also
need their own qualification, loss acknowledgment and rollback policy.

Future publication must reacquire current authority, validate all independent
receipts and credential decryptability, atomically publish the reserved binding
IDs and invalidate snapshots, then explicitly resume. No readiness token or
customer activation endpoint is introduced by this slice. Native x86_64 KVM
acceptance and live provider/application qualification remain release gates.
