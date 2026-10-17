# Successful build to image preparation handoff

For work that already has a checkpoint, use the independent
[durable phase diagnostics](FaasImagePreparation.md).

Applies to `FaasBuildImageHandoffWaiting`,
`FaasImageHandoffRecoveryQueriesFailing` and
`FaasBuildImageHandoffTelemetryUnavailable`. All three warnings are internal.

## Establish reliable evidence

```promql
up{job="imaged"}
imaged_build_handoff_collection_success{job="imaged"}
time() - imaged_build_handoff_collection_last_success_timestamp_seconds{job="imaged"}
imaged_build_handoff_observation_healthy
imaged_build_handoff_pending{job="imaged"}
imaged_build_handoff_oldest_wait_seconds
```

The independent observer reads immediately and every 30 seconds, with a
three-second deadline. Only valid successful observations at most two minutes
old publish backlog and oldest finish time. The healthy recording is one only
with complete, consistent, fresh diagnostics. Failed, stale, future, malformed
or missing evidence stays absent. Known-empty healthy scopes have count and age
zero. Failed scrapes use target-health alerts; siblings cannot repair evidence.

Count covers eligible succeeded build rows with provenance and an artifact path,
whose deployment is pending/building and has no image-preparation checkpoint.
It is not capped at the recovery batch size. Age starts at build completion,
not deployment creation, and grows without new arrivals. A checkpoint removes
work while preparation is active or resumable. Snapshot priming, activation,
release-task waiting and deployment readiness are outside this snapshot.

Keep job and instance. Configured imageds include their producer node and legacy
empty owners. Empty imaged identities retain all-owner compatibility. These
overlapping scopes must never be summed across replicas. Compare the daemon's
configured node with producer routing before diagnosing idle foreign workers.

## Waiting and recovery progress

The wait warning requires oldest age above 300 seconds for five minutes. It can
fire with one build. Checkpointed healthy preparations are excluded. Untouched
builds behind legitimate serial work can still wait too long; this measures delay
and does not establish a stalled daemon.

```promql
imaged_build_handoff_recovery_last_attempt_timestamp_seconds{job="imaged"}
imaged_build_handoff_recovery_last_completed_timestamp_seconds{job="imaged"}
imaged_build_handoff_recovery_inflight{job="imaged"}
imaged_build_handoff_recovery_enabled{job="imaged"}
sum by (job, instance, source, outcome) (
  increase(imaged_build_handoff_recovery_queries_total{job="imaged"}[5m])
)
sum by (job, instance, source, outcome) (
  increase(imaged_build_handoff_recovery_work_total{job="imaged"}[5m])
)
sum by (job, instance, transition) (
  increase(imaged_image_preparation_commits_total{job="imaged"}[5m])
)
```

Recovery runs at startup and on the existing two-second ticker. A pass may execute
16 resumed preparations followed by 16 untouched handoffs synchronously; other
notifications and loop work share that event loop. Missed ticks do not prove
failure. Attempt/completion timestamps are diagnostic only; no new two-second
stalled-loop warning is introduced. Backlog collection remains independent.

Capture imaged logs around list failures, leases, node ownership, publication
and scans. Inspect affected deployments through the existing operator command,
then correlate build finish time, artifact, status and checkpointed work.
Compare [export retention](FaasBuildExportRetention.md) and upstream
[builder worker diagnostics](FaasBuildWorker.md).

## Outcomes and committed progress

Query outcomes are `success|error|canceled` for each `resume|awaiting` source.
The query warning requires errors above 10% of non-canceled calls and at least
three errors in each rolling five-minute window, sustained for five minutes.
Another source/process cannot dilute the ratio. Successful independent snapshot
reads do not prove that recovery list calls work.

Handler outcomes remain separate: `returned` includes nil no-ops; `retryable`
retains recoverable work; `deferred` covers busy leases and protective claim/state
fences; `not_owned` preserves routing; `error` includes image/security failures;
`canceled` matches caller cancellation. These do not drive query-failure alerts.
Check logs and durable state before classifying a handler error as platform failure.

Checkpoint counters advance only after fenced writes succeed. `claimed` includes
token replacement on resume, not unique builds or completed image progress.
`layer_published` and `scan_complete` record those commits. `handed_off` records
the final checkpoint, including release-task ownership, and does not prove a
ready deployment. Shared counters also cover direct OCI image preparation. Nil
returns and terminal/fenced no-ops cannot substitute for committed phases.

## Unknown or stopping observations

The telemetry warning holds five minutes. Inspect deployed version, database
connectivity, read cancellations/deadlines, clocks and metric relabeling first.
Do not interpret absent backlog as zero. Loop cancellation/return disables
expectation and suppresses these warnings without proving a blocked handler
finished or drained.

Existing [stale deployment repair](FaasStaleDeploymentBacklog.md) remains an
independent audited recovery path with its own age and eligibility rules. These
diagnostics do not cancel deployments, force leases, resize batches, change
ownership, restart daemons or deploy anything. Preserve normal recovery fences.
