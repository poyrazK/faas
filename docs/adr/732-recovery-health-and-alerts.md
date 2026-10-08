# ADR-732: Recovery health and alerts

Date: 2026-10-08
Status: Accepted

## Context

Recovery listing and control history explain which jobs exist and who changed them. They do not distinguish scheduler stalls from active capacity waits or warn about pending work approaching expiry. The existing updated_at changes during controls and retries, so it cannot prove admission progress.

## Decision

Persist a nullable last_progress_at and a closed-set wait_reason on recovery jobs. Advance progress only when the scheduler commits an item as queued or skipped, in the same transaction as that item and pacing changes. Pending capacity retries and legacy receipt deferrals update their explicit wait reason without advancing progress. Neither controls, cancellation nor expiry fabricate progress. Existing jobs retain unknown historical progress; observations use creation as an explicit fallback until the first tracked advance. Register both columns as operational for environment cloning.

Expose GET `/v1/apps/{slug}/event-recoveries/health`, requiring existing apps-read/admin scopes and MFA, with no query parameters. Return active jobs only, bounded by the existing three-active-jobs account quota. Read owned non-deleted application and job rows in a repeatable-read transaction; memory reads under its mutex and copies progress timestamps. Terminal jobs remain available through listing and status.

Report current admission state, rate, pending count, last progress, progress age, next attempt, eligibility, overdue duration, expiry risk, and most recent recorded wait reason. Eligibility is the later of scheduled attempt and a fully spent one-second budget window. A running job with pending work is stalled only if both progress/creation baseline and eligibility are at least five minutes old. Resume already schedules no earlier than now, providing a fresh eligibility grace without changing the progress timestamp. Active capacity or legacy receipt retries keep eligibility fresh and remain explicitly waiting. Persistent retries therefore do not count as a scheduler stall; expiry risk still applies.

Pending work is expiring when its deadline is within one hour or already overdue. Running expired jobs awaiting cleanup are reported as expired and remain in the expiry count. Paused jobs are explicitly paused, with a separate paused-expiring count; they are excluded from both running-job alerts. Pacing and wait status are diagnostic observations, not handler outcomes or guaranteed future admission times. Wait reasons remain the last recorded reason until an item advances.

Add app-scoped `event_recovery_stalled_jobs` and `event_recovery_expiring_jobs` metrics to the existing alert evaluator. These observe current counts; rule windows do not aggregate historical samples. Existing comparison, cooldown, notification and recovery behavior apply. Require owned app, no subscription selector, supported 5m/15m/1h/6h/24h window, and webhook action only. Reject deployment-changing actions at API, state and database boundaries. Store read failures mark the observation degraded rather than healthy zero. No alert rules or external notifications are created automatically.

The append-only migration adds progress fields and updates alert metric constraints, preserving existing metrics. Rollback requires removing recovery alert rules first and then drops the progress fields. No historical timestamps are guessed or backfilled.

## Consequences

`events recovery-health APP` and Go/Node/Python SDKs expose the same observations. Operators can diagnose frozen progress, ongoing admission waits, intentional pauses and expiry risk independently. Tracking concerns recovery admission; admitted handler results remain on recovery status/items. The last-progress value is not changed by audit-history writes or user controls.
