# ADR-202610171908: Organization-owned queues and worker bindings

Status: accepted
Date: 2026-10-17

## Context

Company applications, projects, PostgreSQL and buckets retain their resource
namespaces after creator erasure. Queue bindings, environment queue catalogues
and worker policy still reference the human account. A company must retain its
backlog and accepted work without retaining a former employee's authority.

## Decision

Persist immutable organization ownership on queue bindings from their app.
Retain namespace, app/environment, binding and private consumer IDs, queue names,
message bodies, retries, dead letters, replay generations and receipt hashes.
Queue catalogues and their private trigger projections reference resource
namespaces. Reject mixed roots before migration and on subsequent writes.
Unreviewed external triggers, jobs and GitHub/GitOps integrations remain erasure
blockers. Personal erasure purges personal queue records and retains company
queue history. Personal exports exclude company queue references.

Review binding inventory/status, binding configuration, send/receive, legacy
acknowledgement, pull lease/ack/nack/extend, queue state, message/dead-letter
inspection and queue dead-letter replay. Expose organization app aliases and
project/environment workload binding configuration, plus aggregate queue usage.
Owners/admins/developers configure bindings and send/consume messages;
owners/admins replay dead letters; viewers inspect binding/state metadata.
Billing seats may inspect aggregate usage. Message payload inspection requires
an operating role. Credential scopes and MFA remain independent requirements.
Other queue/trigger control routes remain closed to shared access until reviewed.

Use the existing membership revision and transaction fences before mutation
and cached replay. Removal or role changes invalidate in-flight human intents;
rejoining cannot revive old credentials or revisions. Application workload
identity remains app authority: live workers keep using the same receipts and
backlog after creator departure or erasure. Verify organization aliases against
the persisted app, including workload-token requests.

After billing adoption, use the company's plan and financial subject for queue
policy, trigger admission and reserved async concurrency across namespaces.
Capture the quota subject on each invocation reservation and release that
captured subject on completion, cancellation, retry, expiry and deletion.
Move outstanding company reservations atomically at migration/adoption while
preserving other contributions to legacy counters. Limits remain sourced from
`pkg/api/limits.go`; no prices or numerical limits change. Pull leases retain
ADR-1140's per-binding concurrency, visibility and receipt semantics. Existing
app-level queue depth limits remain app-level. Compute billing continues under
the existing organization app financial allocation.

## Acceptance and rollout

Prove memory/PostgreSQL ownership backfill, root isolation, current role/scope
checks, offboarding commit ordering, company quota contention and personal
isolation. Enqueue work, issue a worker receipt, erase the creator, then prove
that a teammate and existing workload identity can consume, acknowledge, retry
and replay the same backlog. A former member's credentials and stale request
must fail. Queue IDs, receipt ownership and accepted workload inputs stay stable.
Prove environment queue admission/delivery survives erasure as well.

Quiesce async admission and dispatch while applying this migration, and restart
all API, scheduler and worker quota writers with the compatible version before
resuming. Old namespace-based quota writers cannot run against organization
reservation snapshots. Rollback requires draining organization reservations and
must precede creator erasure. Native deployment acceptance remains a release
gate; local HTTP and state fixtures do not establish production rollout or KVM
acceptance.

This cutover is a planned-maintenance exception to the online N±1 schema rollout
in [ADR-202610171900](202610171900-release-compatibility-gates.md). Quiesce the
control-plane API and metadata writers as well as async admission and dispatch;
resume them only after every affected reader/writer has been upgraded. Keep
identity erasure disabled until that point. Every ownership migration bounds
lock waits to five seconds. Scoped migration-lint exceptions retain atomic
ownership/name-collision validation and reservation-counter installation;
legacy INSERTs derive their new ownership fields through defaults or triggers.
Production table/index sizes and the required maintenance duration must be
measured and qualified before deployment. These exceptions do not establish
online migration or native runtime acceptance.
