# ADR-20261102183152: Job task output follows the plan's log retention

- **Status:** accepted
- **Date:** 2026-11-02
- **Amends:** ADR-099 (jobs), ADR-213 (durable unified log events)

## Context

A finished job task keeps the combined stdout/stderr tail its VM wrote, up to
1 MiB, in `job_tasks.log_content` (migration 20260912203000297). The attempt
journal (`job_task_attempts`, migration 20260928220000001) copies each
attempt's tail when the attempt ends, so most output is stored twice. Nothing
cleared either column: runs, tasks and attempts are never deleted, and
deleting a job keeps its runs. A recurring job adds one run per cron boundary
and a Scale run can fan out to 5,000 tasks, so stored output grew without
bound, and `docs/DPA.md` promised logs are deleted 90 days after they are
written.

Runtime logs already have a per-plan window: `Plan.LogArchiveRetentionDaysMax`
(Free 1 day, Hobby 7, Pro 30, Scale 90), which meterd's hourly `log_events`
sweep applies.

## Decision

1. **Job task output uses the same window**, the account plan's
   `LogArchiveRetentionDaysMax`, read from `pkg/api` at sweep time. Jobs belong
   to an account (`job_runs.account_id`; no organization owns a job), so the
   plan is `accounts.plan`, the plan schedd admits the job's tasks under. An
   account whose plan is not one of the four gets the shortest window, as
   `log_events` does for an unresolvable plan.
2. **The clock starts when the run finishes** (`job_runs.finished_at`, set
   once its last task is terminal). Every task and attempt of a run finished at
   or before it, so one timestamp ages the run's output together, and a run
   still in flight keeps its finished tasks' output.
3. **Only the output goes.** `log_content` becomes empty and `log_truncated`
   true on the run's tasks and attempts. Runs, tasks and attempts stay, with
   their statuses, exit codes, errors, timings and output manifests. An empty,
   truncated log means "no longer retained"; an empty, untruncated log still
   means the task wrote nothing.
4. **meterd runs the sweep hourly**, next to the `log_events` sweep, in
   bounded batches: up to 1,000 runs per batch, whole runs only, until their
   declared tasks reach 10,000 (at least one run), and at most 1,000 batches a
   pass. `job_tasks` and `job_task_attempts` are written by apid and schedd;
   the sweep never touches status, lease or instance columns, so the job state
   machine is unaffected, as meterd already ages out rows other daemons write
   (`request_telemetry`, `deployment_audit`). The attempt-journal trigger and
   the job task guards only act on status or attempt changes, so clearing the
   output journals nothing.
5. **`job_runs.logs_cleared_finished_at`** records the `finished_at` whose
   output was cleared. The partial index `job_runs_log_retention_idx` holds
   finished runs whose mark differs from their `finished_at`, oldest first, so
   a pass reads only runs not yet cleared and never revisits history. A retry
   that reopens a cleared run gives it a new `finished_at` when it finishes
   again, which makes it due again.

## Consequences

- `gregale job logs` and the attempts list return an empty, truncated log for
  a run past its plan's window. The DPA, `docs/logs.md` and `docs/jobs.md` say
  so.
- A downgrade shortens the window of runs not yet cleared; an upgrade cannot
  bring back output already cleared.
- Account deletion still removes every job row with the account.
- `meterd_job_logs_retention_cleared_total{table}`,
  `meterd_job_logs_retention_last_success_timestamp_seconds` and
  `meterd_job_logs_retention_failures_total` report the sweep.
- The first pass after deploy clears the backlog of runs already past their
  window, up to 1,000 batches an hour.
