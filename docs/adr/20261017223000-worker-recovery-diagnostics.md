# ADR: Worker recovery diagnostics and stalled-recovery alerts

Date: 2026-10-17
Status: Accepted
Related: [Durable worker-pool recovery](20261017221000-durable-worker-recovery.md)

## Context

Durable worker recovery preserves retries and app leases, but its outcome is not
included in the operational summary. Operators must query Postgres to distinguish
admission refusal, startup failures and an overdue sweep. Queue backlog alerts do
not cover singleton workers with no incoming messages. Recomputing demand in apid
would duplicate the scheduler's environment, queue, cooldown and captured-setting
rules and could show a different target from the actual admission plan.

## Decision

Add an optional `recovery.workers` object to the existing authenticated app
operational-summary API. Include the app-wide last outcome, failure count, next
attempt, update time and reconciliation lease deadline. Explicitly identify retry
scope as `app`; selecting an environment filters pool observations only. Omit
claim tokens, revision hashes and free-form errors. Use the existing ownership,
read-scope and environment-identity checks, without any wake or scheduling calls.

Store diagnostic pool observations in an additive `worker_recovery.pools` JSON
array under the existing claim/completion fence. Each entry records environment,
target deployment, desired/running/starting/draining counts and observation time.
The target comes from the completed scoped demand plan after scaling cooldowns
and stabilization, before account or physical-capacity refusal. Running means the
target generation; predecessor, stopping and other resident transitional workers
count as draining. Counts describe the recorded instant, not a new health check.

Publish observations after a complete planning read, including after an admission
or startup failure. Retain the previous observation and its original timestamp
when planning or the final fleet read fails. A scoped pass replaces only its own
environment; a full pass replaces the set. A desired revision change invalidates
previous observations. Record the revision in each entry as well, so an older
scheduler updating the app revision cannot make old pool counts current.
Drop observations from an earlier revision before completing a newer attempt,
including when a dependency read failed. Diagnostic leftovers must never prevent
the current claimant from releasing its lease or persisting its retry deadline.
Successful scoped events retain the existing full-app sweep deadline, so a busy
environment cannot postpone observation or recovery of a quiet neighbor.

Missing observations remain unknown. A pool observation becomes stale after
900 seconds. A fresh environment cannot make another environment's stale
observation known. API reads retain stale observations with an explicit flag;
fleet capacity metrics exclude them. `gregale inspect` renders app-wide retry
state, environment counts and timestamps. Watch comparisons ignore observation
clock churn while retaining changes in counts, eligibility, staleness and retries.

Every recovery sweep collects owner-scoped database aggregates, including apps
with no ledger row. Exclude inactive apps, suspended/deleted accounts and abuse
holds using discovery's eligibility rules. Export only closed label vocabularies:

- `schedd_worker_recovery_apps{condition="eligible|unknown|overdue"}`.
- `schedd_worker_recovery_replicas{state="desired|running|starting|draining|missing"}`.
- `schedd_worker_recovery_observed_timestamp_seconds` for successful collection.

Sum missing capacity per pool, so surplus in another environment cannot cancel a
deficit. Overdue means both the next attempt and lease deadlines have elapsed by
60 seconds; a never-claimed app uses its creation time. Successful empty
collections replace all counts with zero. Failed collections retain their old
timestamp, preserving unavailable evidence across database failures.

Ship internal warning alerts for missing required workers for ten minutes,
overdue reconciliation for two minutes, and unavailable observations for five
minutes. Capacity/stall alerts require database collection within 60 seconds;
unavailable collection has its own alert. A desired target of zero contributes no
deficit. Existing daemon scrape/down alerts cover missing or stopped exporters.
Limits live in `pkg/api/limits.go`; alert expressions and fixtures use the same
60-second freshness boundary. These diagnostics do not authorize recovery work.

## Compatibility and verification

Apply the observation migration before upgrading schedd and apid. The column has an empty
array default; old binaries retain their existing explicit-column queries and
completion updates. Optional API fields remain compatible with older clients and
servers. During an older-scheduler rollback, pool observations eventually become
unknown, while its existing retry behavior continues. Leave the additive column
in place until all upgraded schedulers have stopped.

Shared memory/Postgres contracts cover observation round trips, copy isolation,
revision fencing, ownership, inactive accounts/apps, stale neighbors, future
retries and active/expired leases. Scheduler restart tests verify a capacity
refusal, persisted diagnostics and automatic clearing after capacity returns, as
well as scoped scale-in and zero floors. API tests verify environment filtering
and mutation-free reads; CLI tests verify human/JSON output and watch behavior.
Promtool fixtures exercise alert onset, short failures, unavailable evidence and
recovery. These are portable diagnostics checks; native worker availability
qualification remains the separate requirement of the recovery ADR.
