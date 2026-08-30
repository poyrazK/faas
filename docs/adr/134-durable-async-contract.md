# ADR-134 · Durable async-job contract (shared `pkg/dispatch`)

- **Status:** accepted
- **Date:** 2026-08-29
- **Issue / PR:** mega-PR (PR-A through PR-E as atomic commits) on
  branch `worktree-feat-dispatch-contract-pr-a`
- **Supersedes / amends:** none
- **Decision:** Introduce a shared `pkg/dispatch` package that every
  durable background-job producer (async invocations, delayed tasks,
  queues, cron, trigger records) consumes. Ship the contract first
  (PR-A), then migrate individual producers incrementally. Do NOT
  merge the two schedd drains (`drain.go` for `invocations`,
  `dispatch_triggers.go` for `trigger_records`) — keep them as
  separate FSMs but feed both from the same `dispatch.Job`,
  `dispatch.RetryPolicy`, `dispatch.Lease`, `dispatch.DeadlinePolicy`
  types.

## Context

Gregale's durable background jobs had drifted into 8 of 10
semantic guarantees with no shared contract:

| # | Capability | Today (pre-PR) |
|---|---|---|
| 1 | Idempotency keys | ✅ apid middleware |
| 2 | Durable leases | ⚠ `instances.lease_token` only |
| 3 | Explicit retry policy | ⚠ per-trigger `max_attempts`; async uses plan-wide `MaxQueueAttempts` |
| 4 | Exponential backoff with jitter | ✅ `pkg/sched/dispatch_triggers.go::computeRetryBackoff` |
| 5 | Cancellation | ✅ `CancelInvocation` |
| 6 | Deadline / timeout | ⚠ per-job `task_timeout_s`; sync long-poll hardcoded Free 5s / paid 30s |
| 7 | DLQ replay | ⚠ trigger_replay ✅, queue DLQ replay ❌ |
| 8 | Per-account concurrency | ⚠ per-app `MaxQueueDepth`; no account-level cap |
| 9 | Result retention | ⚠ `pkg/sched/retention.go` only prunes `instances`; `invocations` unbounded |
| 10 | Shared dispatch contract | ⚠ `invocations` drain unified for async/queue/delayed/cron; `trigger_records` separate FSM |

The five `⚠` rows could each be fixed piecemeal — but doing them
independently would lock in five slightly-different type shapes for
"a retry policy", "a deadline", "a lease". The platform's M7
financial-model ladder assumes one counter per account (per-plan),
and the M7 §17 gap register lists `G-Async-Retention` and
`G-Account-Concurrency` as open.

A clean answer is a contract package — `pkg/dispatch` — that every
drain consumes. The user's direction matches: *"start with a shared
dispatch contract and common state transitions, then migrate
individual producers gradually."*

## Decision

Five atomic commits (one per PR in the mega-PR), each
independently reviewable:

1. **PR-A — `pkg/dispatch` package** (no migration). Five files:
   `dispatch.go` (JobKind, RetryPolicy, DeadlinePolicy, Lease types),
   `lease.go` (Leaser[T] interface stub), `contract.go` (Job interface
   — Kind / ID / AppID / AccountID / Source / RetryPolicy / Deadline /
   CurrentAttempts / LastError / Snapshot), `backoff.go`
   (`(*RetryPolicy).Backoff(attempt) time.Duration` — same curve as
   today's `computeRetryBackoff`), `doc.go` (package docs).
   `pkg/sched/drain_compile_test.go::TestDispatch_ContractCompiles`
   asserts `var _ dispatch.Job = (*state.Invocation)(nil)` at compile
   time. The drain + dispatch_triggers both import the new types;
   no behavior change.

2. **PR-D — `pkg/lease` extraction** (no migration). Generalize the
   CAS-with-token pattern from `instances.lease_token`
   (`pgstore.go:2656`) into `pkg/lease.Manager` (Acquire / Renew /
   Release / ClaimWithLease). All SQL is `UPDATE ... WHERE
   lease_token=$N AND lease_expires_at<now() RETURNING lease_token`;
   zero rows → `ErrConflict`. Migrating watchdog + migration handoff
   call the new manager; behavior identical.

3. **PR-B — `invocations` per-row fields + reaper + counter-table**
   (migrations 517-519). Three new columns on `invocations`
   (`deadline_at`, `retry_policy JSONB`, `result_retention_until`),
   new `account_async_quota` counter table, three new `Limits`
   fields (`MaxAsyncInvocationsPerAccount`,
   `MaxAsyncInvocationDeadlineSeconds`,
   `MaxAsyncResultRetentionSeconds`), OpenAPI + SDK regen, drain
   wiring (`ClaimInvocationWithCap` atomic increment + cap check),
   `pkg/sched/retention_invocations.go` 60s reaper.

4. **PR-C — `trigger_records` migration + queue DLQ replay**
   (migrations 520-522). Same three columns on `trigger_records` +
   `replayed_from_invocation_id` + `last_replayed_at` on
   `invocations`. `dispatch_triggers.go::computeRetryBackoff` becomes
   a wrapper over `dispatch.RetryPolicy.Backoff(attempt)`. New
   handler `cmd/apid/handlers_queues_replay.go` for
   `POST /v1/apps/{slug}/queues/dead_letter/{id}/replay`.

5. **PR-E — `trigger_records` reaper** (migrations 523-524).
   `pkg/sched/retention_triggers.go` 5-minute tick, batch 1000,
   terminal-state rows with `result_retention_until < now()`.

## Why a wrapper type for `state.Invocation` to satisfy `dispatch.Job`

Go forbids a field and a method of the same name on a struct, and
`Job.ID()`, `Job.AppID()`, `Job.AccountID()` collide with the
`Invocation` row's primary-key columns. `state.InvocationJobAdapter`
embeds `Invocation` and proxies the three accessors; the other six
methods are inherited via embedding. Compile-time check lives at
`pkg/sched/drain_compile_test.go::TestDispatch_ContractCompiles`.

## Why counter-table not live-count

The per-account cap must be advisory across producers (async + queue
+ delayed + cron all share the same `current_inflight` counter).
Live-count would require `SELECT COUNT(*)` per claim with an
unlocked window — fine at low QPS, broken at the §12
200-invocations/sec/tenant target. Counter-table is one
`UPDATE ... WHERE current_inflight < max_inflight RETURNING` row
update per claim, atomic, no TOCTOU.

## Why two drains, NOT merged

The unified `invocations` drain (async / queue / delayed / cron
share one row type) is migrated to `dispatch.Job` end-to-end. The
`trigger_records` drain keeps its own FSM but consumes the same
`dispatch.RetryPolicy`, `dispatch.Lease`, `dispatch.DeadlinePolicy`
types — see PR-C. Merging them now would require aligning two
distinct SQL schemas (`invocations` vs `trigger_records`) and would
risk regressing the trigger_records replay path (which has
operator-side audit metadata the invocations row type does not).

## Migration slot dance

PR-B claims slots 517-519; PR-C claims 520-522; PR-E claims 523-524.
PR-A + PR-D ship with no migration (pure refactors). Real
migrations land at 518, 519, 521, 522, 524; the
`00XXX_reserve_slot.sql` fences are placeholders so a sibling PR
branching from main does not accidentally land a real migration at
one of those slots.

## Spec compliance

Every `Limits` field added in ADR-134 lives in `pkg/api/limits.go`
and is mirrored in `limits_test.go`'s 4-row parity table
(Free / Hobby / Pro / Scale) — no inline limits anywhere in the
codebase. Test `TestPlanLimitsMatchSpec` is the gate; it fails CI
if any row drifts.

## Consequences

Positive:
- One canonical retry / lease / deadline type across producers.
- A counter-table pattern that scales to §12's 200 rps/tenant target.
- A compile-time check that breaks the build if a new producer
  doesn't satisfy the contract.
- §17 gaps `G-Async-Retention` + `G-Account-Concurrency` close.

Negative:
- 5 atomic commits land as one mega-PR. Reviewer load is higher
  than typical (~10 min/commit × 7 = 70 min).
- `state.InvocationJobAdapter` is one extra type per row type that
  wants the `dispatch.Job` interface — the convention going forward
  is to add such an adapter whenever a row type's primary-key
  columns would collide with the contract method names.

## Follow-ups

- **Per-app cap as product surface** — `Limits.MaxQueueDepth` is
  per-app today. The user can dial down individual apps below the
  account cap; surfacing this on the dashboard is a separate,
  customer-facing PR.
- **Retry-policy as JSONB column on `apps`** — global retry-policy
  defaults per app, overridable per invocation. Not in scope here.
- **Trigger_records replay chain via `Source` column** — the
  `invocations` row stamps `Source=InvocationReplay` on replay;
  the trigger_records side has the same `Source` field but
  operator-replay creates a new row rather than mutating the
  dead-lettered one. Future ADR if parity is required.
