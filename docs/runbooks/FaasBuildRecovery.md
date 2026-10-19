# Running builds and builder VM cleanup recovery

Applies to `FaasBuildRunningOverdue`, `FaasBuildVMCleanupOverdue`,
`FaasBuilderReaperFailing`, `FaasBuilderReaperStalled`,
`FaasBuildRecoveryTelemetryUnavailable` and
`FaasBuilderReaperTelemetryUnavailable`. All are operator-internal warnings.

## Read the evidence

Apid observes the entire database fleet every 30 seconds. Each count and oldest
timestamp comes from the same snapshot; each apid replica independently sees
the fleet. Do not sum counts across replicas. An observed empty set has count
and timestamp zero. Missing, failed, inconsistent or older-than-120-second
observations are unknown, never empty. The telemetry warning is independent of
whether any work is queued, running or awaiting cleanup.

```promql
faas_build_recovery_observation_healthy
faas_build_running_count{job="apid"}
faas_build_running_oldest_age_seconds
faas_build_vm_cleanup_pending{job="apid"}
faas_build_vm_cleanup_retry_pending{job="apid"}
faas_build_vm_cleanup_oldest_age_seconds
apid_build_recovery_collection_success{job="apid"}
time() - apid_build_recovery_collection_last_success_timestamp_seconds{job="apid"}
```

Running age begins at the current claim's `started_at`; requeue removes it from
running observation and a later claim starts a new running age. Queue wait
retains the original enqueue time in the separate queue metrics. Cleanup age
begins at durable obligation creation and survives failed attempts, live claims
and fenced late results. Cleanup pending includes claimed rows and cancellation
obligations even if no running build remains. Retry pending counts outstanding
rows with a recorded failed stop, not lifetime attempts.

Each builder's progress is process-local and resets on restart:

```promql
builderd_build_reaper_observation_healthy
builderd_build_reaper_last_attempt_timestamp_seconds{job="builderd"}
builderd_build_reaper_last_completed_timestamp_seconds{job="builderd"}
builderd_build_reaper_last_success_timestamp_seconds{job="builderd"}
builderd_build_reaper_last_pass_success{job="builderd"}
increase(builderd_build_reaper_passes_total{job="builderd",outcome="error"}[30m])
increase(builderd_build_reaper_recovered_builds_total{job="builderd"}[30m])
increase(builderd_build_vm_cleanup_operations_total{job="builderd",outcome="error"}[30m])
```

Recovered-build counts mean rows atomically changed from running to failed.
They do not prove VM teardown. Cancel success means vmmd accepted/completed the
stop call; record success means the store accepted the result, which may be a
fenced no-op. Only a subsequent fresh backlog snapshot establishes which
durable obligations remain. Error strings and build IDs stay in existing logs,
not metric labels.

## Interpret timing

`builderd_build_reaper_threshold_seconds` is the effective normalized threshold,
including the build-timeout/host-teardown margin. Do not infer it from outdated
comments or the guest timeout alone. `builderd_build_reaper_interval_seconds`
is the actual sweep cadence.

Running age warns beyond threshold plus two intervals for five minutes. Cleanup
age warns beyond two intervals for five minutes. The fleet allowances use the
largest configured value among discovered builders, and disappear if any such
target is down or lacks usable configuration. An alert can describe a large
active batch; compare pending count, age and successful operation rate before
concluding the loop is stuck.

The latest unsuccessful completed pass warns after five minutes, even if its
row sweep succeeded. A pass still in progress does not change that result.
The separate stalled warning requires no whole-pass success for two intervals
plus the full serial cleanup call budget, then five minutes. The current budget
is 64 rows times a 30-second cancel and 30-second record timeout: 3840 seconds.
At a ten-minute interval this gives 84 minutes plus the five-minute hold. This
conservative limit avoids calling a legitimate slow batch a stalled loop.
Before the first success, loop initialization supplies the grace reference;
zero last-success alone does not mean healthy or failed.

## Investigate and recover

1. For telemetry warnings, check `/readyz`, database access, scrape health,
   clocks and apid `build recovery observation failed` logs. Missing builder
   config makes timing unknown; another builder cannot repair its evidence.
2. For an unsuccessful pass, inspect builderd `stuck-running sweep`,
   `builder VM cleanup` and `record builder VM cleanup` warnings. Distinguish
   sweep/claim failures from vmmd cancellation errors and result-record errors.
3. For overdue running builds, compare current claim age with effective settings
   and inspect builderd/vmmd health and reaper progress. A running database row
   does not establish that its VM is alive.
4. For cleanup backlog, compare creation age, retry-pending count and cancel vs
   record outcomes. Check vmmd connectivity and database writes. The durable
   queue retries on the existing sweep cadence; do not delete obligations or
   overwrite claim tokens to clear an alert.
5. Capture logs and goroutine evidence before restarting a stalled builder.
   Preserve build state and cleanup obligations. Existing claim fencing and
   retries own recovery; these metrics perform no remediation.

Recovery is shown by fresh valid observations, a successful whole pass, and
decreasing/drained backlog. A stopped poller suppresses backlog-age assertions
and raises telemetry unavailable; it does not prove recovery. These rules assume
one deployment/database in the Prometheus scope. They cannot infer an entirely
undiscovered builder installation; use existing discovery and daemon alerts.
