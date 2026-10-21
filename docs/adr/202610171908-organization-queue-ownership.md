# ADR-202610171908: Organization-owned queues and worker bindings

Status: accepted
Date: 2026-10-17
Amended: 2026-10-19 (online rollout; replaces the planned-maintenance exception)

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
Move outstanding company reservations to the company counter (pooling) one
release after the capture ships, preserving other contributions to legacy
counters (see Rollout). Limits remain sourced from
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

Native deployment acceptance remains a release gate; local HTTP and state
fixtures do not establish production rollout or KVM acceptance.

## Rollout (amended 2026-10-19)

The original cutover was one transaction per migration under a declared
"planned-maintenance exception": quiesce writers, move reservations
atomically. The rc.277 rollout showed why that cannot ship: migrations run
before activation, beside the previous release's writers, and
`20261018003100003` deadlocked against them. ADR-202610171900 amendment 1
withdraws the exception. The ownership schema now lands online in one full
release:

- `20261018003100003` to `…007` never ran in production and are ledger
  placeholders. `20261019190114207` to `…676` apply the same schema as
  `-- +goose NO TRANSACTION` files. Every step is its own transaction on at
  most one existing table (plus the table a new foreign key references),
  waits at most 500 ms for a lock and retries. 500 ms is below
  `deadlock_timeout`, so when a step and a writer wait on each other the step
  gives up first and the writer proceeds. Constraints are added `NOT VALID`
  and validated in a later step; replaced foreign keys are swapped add, then
  validate, then drop and rename. Backfills commit every batch. `SET NOT NULL`
  follows a validated `CHECK`, and indexes are built concurrently.
- The previous release's INSERTs and UPDATEs never set the new ownership
  columns; triggers derive them. Triggers that forbid changing an ownership
  column are installed after their table's backfill, under one short write
  lock that also covers rows written in between. Triggers that write an
  organization id into `account_id` are installed only after every foreign
  key on that write path references `resource_namespaces`.
- Every step is idempotent: a failed run resumes from the start of its file,
  and a database that applied the original migrations converges on the same
  schema.

**Async reservations: capture now, pool next release.** The previous
release reserves and releases on the invocation's namespace counter
(`account_async_quota.account_id = invocations.account_id`). If a company
reservation moved to the company counter while such a writer runs, its
release would decrement the namespace counter instead and both counters
would drift for good: nothing reconciles them. So this release:

- captures `async_quota_subject_id` on every reservation (a trigger stamps
  it for the previous release's claims; open reservations are backfilled to
  their namespace);
- keeps every reservation, company or personal, on the namespace counter.
  `invocation_async_subject` returns the organization only once
  `orgs.async_quota_pooled_at` is set, and claimants follow it, so the
  counter a claim increments is always the subject the trigger records;
- releases by the captured subject (the code already did), which is right
  for either counter;
- moves nothing at adoption. Company plan, status and abuse policy still
  apply to company work; only the concurrency counter stays per namespace,
  as in the previous release.

The release after this one, when no writer releases by namespace any more,
pools each adopted organization with `adopt_org_async_reservations(org)`:
one short transaction per organization that locks the organization row (as
claims do), sets `async_quota_pooled_at`, locks the open reservations and
moves their counts, in completion's row-before-counter order. Adoption then
pools in the same transaction. Rolling back from that release to this one
is safe, because this release already claims and releases by the subject.

Account hard-delete in the previous release relies on the old
`ON DELETE CASCADE` from `accounts`. After the swap those children reference
`resource_namespaces`, so an old-binary purge during the rollout window or
after a rollback removes the account row and leaves its namespace rows; the
erasure guards still refuse a purge while company or unreviewed resources
exist. Keep identity erasure disabled until the fleet runs this release, as
before.
