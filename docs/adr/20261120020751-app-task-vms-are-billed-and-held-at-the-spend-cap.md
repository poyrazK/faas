# ADR-20261120020751 · App task VMs are billed and held at the spend cap

- **Status:** accepted
- **Date:** 2026-11-20
- **Amends:** ADR-230 (deployment-attached app tasks), ADR-1162 (spend cap
  holds queued work)
- **Related:** spec §4.7 (billing uses plan RAM + 8 MB per running second)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

The cost-control review of 2026-11-19 (finding N16) found that an app task
(a manual command, a release command or a command cron) was outside every
cost control:

- **Unbilled.** A task boots the app's deployment in its own VM at the app's
  RAM for up to an hour. schedd reserves it in the node ledger
  (`KindAppTask`) but writes no `instances` row, and meterd bills only
  instance rows. Task VMs accrued no usage, so they never reached the Free
  allowance, the spend cap, budgets or the invoice. `app_tasks` is on for
  every account in production.
- **Not held by the spend cap.** `ResolveAppTaskRuntime` checked strict and
  preview budget holds but not the spend cap. Wakes, forks, jobs, warm pools
  and crons all check it. (Command crons were already held: the cron sweep
  checks the cap before any cron fires.)

## Decision

1. **meterd bills each task VM like an instance.** Every sampled minute it
   lists the tasks whose run overlaps the minute
   (`AppTasksInBillingWindow`, indexed on `started_at`) and bills the
   overlapping seconds at the app's RAM + 8 MB, under the app's billing
   subject. The usage row is keyed by the task ID, so rolling a minute again
   rewrites the same row. A task bills from `started_at` (the command
   starts) to `finished_at`. A task that recorded no finish stops billing at
   its last update once terminal, else at the earliest of its lease expiry,
   its timeout plus two minutes, and now, so a lost runner cannot bill
   forever.
2. **The spend cap holds a task before it boots.** `ResolveAppTaskRuntime`
   refuses a task whose billing subject is at its cap with 402
   `admission_refused`. Nothing is dispatched; like a held budget, the
   coordinator leaves the restoring lease to the sweep, so the task waits and
   starts once the cap lifts. A command already running finishes within its
   timeout.

## Consequences

- Customers are billed for the RAM their commands use, as the docs now say.
  The restore before the command starts is not billed (as for a job task's
  boot), and egress from task VMs is not metered yet.
- A release command at the cap waits, so the deployment it gates waits too;
  the app could not wake at the cap anyway.
- Retained cost evidence attributes task usage to the app and project; it
  has no deployment, since a task has no instance row.

## Rejected

- **Write an `instances` row for every task VM.** schedd is the only writer
  to `instances` and its state machine assumes serving replicas; a task row
  would enter routing, reaping and floor math. The task's own lifecycle is
  already durable and exact.
- **Fail a capped task at once.** Queued work waits at the cap everywhere
  else (ADR-1162 amendment 2026-11-03); failing tasks would also fail
  deployments whose release command was merely queued.
