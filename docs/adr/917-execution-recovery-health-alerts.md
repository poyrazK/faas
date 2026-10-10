# ADR-917: Execution recovery health and alerts

Date: 2026-10-09
Status: Accepted

## Context

Admission health stops listing a job when its admission finishes. Its admitted
handlers may still be queued, running or retrying, or lack trustworthy terminal
evidence. Execution-finished notification waits for saved exact replay results.
Operators need visibility into that wait before the retained job disappears.

## Decision

Extend the existing app recovery-health endpoint and CLI with an optional
`execution` section. Existing admission fields keep their meaning. Select retained
execution-mode jobs with completed/cancelled admission, no execution-finished
stamp, and at least one queued item lacking a saved result matching replay UUID,
generation and creation time at the observation time. Include historical jobs
that cannot emit a new notification; expose `notification_pending` separately.
Zero-admission and fully saved jobs are excluded, even before notification sweep.
No health read captures results, stamps completion or enqueues a notification.

Inspect the oldest 50 candidates, using one extra candidate to detect truncation.
Return up to three oldest diagnostics. Aggregate counts describe observed jobs,
not delivery counts. Waiting includes all unresolved jobs; prolonged, unknown
and retention-risk counts overlap. Missing replay identity contributes to unknown
and untracked counts. Known terminal observations without saved confirmation
appear in `awaiting_saved_results_count`. Wait age starts at admission completion:
15 minutes is a prolonged wait, not proof that a handler or scheduler is stalled.
Retention risk begins 24 hours before the nominal 30-day completed-job boundary;
pruning may occur later. Parent-child links remain available on diagnostic jobs.

PostgreSQL admission and execution observations share a repeatable-read, read-only
snapshot. A partial app/account completion index narrows candidates; saved-result
lookups use existing job/position identities. Inspecting candidates can still
scan retained item metadata. Bound the whole read to the existing five-second
request timeout and each observed job to the existing 10,000-item admission limit.
Memory storage mirrors selection and classification under its existing lock,
retaining only 51 sorted candidate references while scanning retained jobs.

Add webhook-only app metrics `event_recovery_execution_waiting_jobs`,
`event_recovery_execution_prolonged_wait_jobs`,
`event_recovery_execution_unknown_jobs` and
`event_recovery_execution_retention_risk_jobs`. They use existing evaluation
windows, cooldown and recovery-notification behavior; windows do not aggregate
these snapshot gauges. Incomplete counts are lower bounds: only a satisfied
`gt`/`gte` comparison can fire from them. All other partial observations degrade
without asserting recovery. Missing execution support and read failures degrade.
No alert rule is created automatically.

## Rollout and downgrade

Apply migration `20261009225025192` before API/evaluator binaries. It widens metric
and scope constraints and adds the partial index. OpenAPI, embedded schema and
Node/Python SDK models expose the additive section; old clients can ignore it,
and new clients support older servers omitting it. Remove new-metric alert rules
and roll back binaries before Down; Down rejects remaining new rules and restores
the exact preceding metric vocabulary, including retention health metrics.

## Consequences

Large backlogs cannot produce a false healthy zero or recovery notification from
partial observations. These diagnostics are bounded retained evidence, not a
complete fleet counter or historical time series. An app with expensive retained
metadata can exhaust the read budget and degrade alerts. This work adds no worker,
result writes, table or columns and therefore no operational clone registry entry.
