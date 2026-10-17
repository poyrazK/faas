# Release-command and app-task runner diagnostics

Applies to FaasAppTaskTelemetryUnavailable, FaasAppTaskRunnerStalled,
FaasReleaseTaskQueued, FaasAppTaskRecoveryOverdue, FaasAppTaskEvidenceUnknown
and FaasAppTaskOperationsFailing. These are internal warnings. They do not
retry commands, bypass admission, change public status or enable dispatch.

## Trust the evidence

Require `up{job="schedd"} == 1` and
`schedd_app_task_observation_healthy == 1` on the same job/instance. The independent
observer reads all nonterminal app tasks at startup and every 30 seconds, with a
three-second deadline and a 120-second freshness ceiling measured from read
start. Scrapes read RAM only. A failed read immediately withdraws counts and
ages; a missing, stale or inconsistent observation is unknown, never empty.
Inspect the bounded `schedd: app task observation failed` log, database health,
clocks, binary version and scrape configuration. Missing initialized counters or
invalid runner gauges also make telemetry unavailable.

The durable observation matches the shared fleet claim queue, with no node filter
or task-list pagination cap. Replicas overlap: never sum them. Local slot and
progress gauges describe only the coordinator in that process. Do not subtract
local slots from fleet queue counts. Active tasks remain visible when their app
is deleted, until cancellation and teardown are acknowledged or leases recovered.

`schedd_app_task_observation_enabled == 0` withdraws expectation on observer
cancellation/return. This does not prove that tasks or VMs drained.
`schedd_app_task_dispatch_enabled == 0` means the explicit
`FAAS_APP_TASK_DISPATCH=1` opt-in is absent. Queue visibility remains available;
runner, queued-release, recovery and operation warnings exclude that intentional
opt-out. Do not enable dispatch as an alert remediation without the normal
runtime/rollout review.

## Read task cohorts

Each fixed kind (`release`, `manual`, `cron`) and class exposes
`schedd_app_task_pending`, `schedd_app_task_age_unknown` and
`schedd_app_task_oldest_boundary_timestamp_seconds`. Unknown age is a subset of
pending. Timestamp zero means there are no known ages, not zero wait. The
recording `schedd_app_task_oldest_known_boundary_age_seconds` is absent for an
unknown-only cohort and zero for proven empty work.

| Class | Meaning and known boundary |
| --- | --- |
| queued | Currently claimable; max of creation, last durable update/requeue and due retry time |
| retry_wait | Queued but retry is not due; age intentionally unknown |
| held | Queued behind managed-Postgres admission, stale exclusive ownership or deleted-app intent; age intentionally unknown |
| deadline_overdue | Queued beyond its start deadline without an already-started occurrence; deadline is the boundary |
| restoring | Claimed before the running fence; age intentionally unknown |
| running | Dispatch fence persisted; command start is the boundary |
| cancellation_pending | Cancellation requested and not acknowledged; request is the boundary |
| lease_expired | Restoring/running lease expired, including pending cancellation; expiry takes precedence |
| unknown | Missing, invalid, nonfinite or future lifecycle evidence; age unknown |

These are observation classes, not new task statuses. The queued predicate
includes due retries, start-deadline/occurrence state, exclusive generation,
lease and attempt deadline, and the managed-Postgres admission fence. A boundary
is not continuous class residence: a held task becoming eligible has no durable
eligibility-change clock. Renewal overwrites `updated_at`, so neither it, task
creation nor an assumed lease duration can reconstruct restore start time.
Expected unknown restoring/retry-wait/held timing does not fire the evidence
warning. Future lease expiries and future retry/start deadlines are legitimate;
future creation/update/start/cancellation evidence is not.

FaasReleaseTaskQueued requires a currently claimable release boundary older than
five minutes for five minutes. It excludes intentional gates and disabled
dispatch. This is a triage threshold, not a customer execution SLO. Running
commands can legitimately last up to their configured timeout, including the
interactive attach budget; there is no generic long-running-task alarm.

## Check coordinator progress

Compare `schedd_app_task_runner_running`, `schedd_app_task_slots_in_use` and
`schedd_app_task_slots_configured` on the affected target. Slots cover the full
claimed handler, including restore, execution, teardown and acknowledgement.
They do not count VMs. Cancellation immediately withdraws runner-running even
while cleanup occupies a slot.

The claim and sweep `*_last_success_timestamp_seconds` gauges record actual
successful calls, including empty results. A completely occupied pool need not
poll for more work. Sweeps must continue independently. FaasAppTaskRunnerStalled
requires enabled dispatch and a stopped runner, no successful sweep for two
minutes, or free slots without a successful claim query for two minutes,
sustained for five minutes. Fresh durable observations do not establish worker
health. Inspect the coordinator startup/exit, database pool/locks, worker errors,
lease renewal and backend RPC health. Do not change concurrency or lease timeouts
solely to silence the warning.

`schedd_app_task_operations_total{operation,outcome}` counts actual coordinator
calls: claim, sweep, renew, attach_read, restore, running_fence, attach_route,
execute, destroy and complete. Outcomes are a closed vocabulary: success, empty
(claim only), error, canceled, lease_lost and cancellation_pending. Empty polls,
parent cancellation, lost ownership and the completion cancellation race are
separate from errors. Interrupted gRPC returns use the call context even when
the transport status does not wrap its error. Command deadline errors count as canceled calls;
restore, destroy and finalization budget failures remain errors. The persistent-operation warning requires more than 10%
errors and at least three errors per five-minute window, sustained five minutes.
Restore errors include account/policy refusals as well as preparation failures;
inspect bounded error-class/stage/grpc-code logs before assigning infrastructure
blame. Destroy/finalize deadline failures remain failures, even during shutdown.

`schedd_app_task_results_total{kind,status}` records successful durable completion
acknowledgements using the returned row, including queued policy retries. It
excludes sweeper changes and is not a command-delivery or infrastructure-error
counter. A nonzero customer exit, command timeout or cancellation is a task
verdict; a transport failure is a different operation result. Successful calls
and durable acknowledgements do not prove publication, readiness or promotion.

## Investigate cancellation and lease recovery

FaasAppTaskRecoveryOverdue requires cancellation, lease expiry or missed start
deadline age greater than one minute for five minutes. Compare successful sweep
progress and `schedd_app_task_recovery_total` changes. That counter reports only
the existing sweep result fields: requeued_restores, failed_runs and cancelled.
It does not infer policy retries omitted from the sweep result.

Use the supported task/deployment evidence to identify affected work and inspect
pinned deployment/artifact, durable status, lease ownership, cancellation and
bounded failure code. Commands, arguments, output, account/app/task IDs, lease
tokens and error messages are never metric labels. Do not copy secrets or raw
command output into incidents.

Preserve the existing running fence and teardown-before-acknowledgement contract.
An expired restore is safe to recover before dispatch. An expired running task
has an uncertain command outcome; follow its existing durable failure/retry
policy. Do not manually replay it, clear fences, extend leases, forge timestamps
or mark it successful to unblock a deployment.

For a deployment classified `release_blocked`, correlate its internal release
task with [snapshot-prime diagnostics](FaasSnapshotPrime.md). Missing release
intent, a failed command or an incompatible artifact/command pin is a gate
finding, not an eligible-queue delay. After a successful pinned release, follow
[snapshot capture](FaasInitSnapshotCaptureFailures.md) and
[publication/activation](FaasPostCapture.md). No new API, frontend or rollout
behavior is introduced here.

Command schedules before task admission have separate [cron admission diagnostics](FaasCommandCrons.md).
