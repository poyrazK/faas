# Durable image-preparation phase diagnostics

Used by `FaasImagePreparationTelemetryUnavailable`,
`FaasImagePreparationPhaseProlonged` and `FaasImagePreparationPhaseAgeUnknown`.
All are internal warnings. They do not change public status or retry work.

## Establish trustworthy evidence

Check `up{job="imaged"}` first; failed scrapes use existing target-health alerts.
Per job/instance, require `imaged_image_preparation_observation_healthy == 1`.
The independent observer reads immediately and every 30 seconds with a three-second
deadline. Require successful collection no more than 120 seconds old, complete
phase metrics and coherent counts/times. Failed, missing, future, inconsistent or
expired evidence means unknown, not zero work. Inspect the log message
`imaged: image preparation progress observation failed`, database health, clocks,
migrations and the deployed binary/scrape configuration.

`imaged_image_preparation_observation_enabled == 0` means cancellation or loop
return withdrew expectation; pending work need not have drained. Fresh cached
counts can remain during shutdown. Absence is not completion.

Each fixed phase exposes:

| Metric | Meaning |
| --- | --- |
| `imaged_image_preparation_pending` | All eligible active/resumable checkpoints, without a batch limit |
| `imaged_image_preparation_phase_age_unknown` | Pending subset without historical phase timing |
| `imaged_image_preparation_oldest_phase_started_timestamp_seconds` | Oldest KNOWN phase start; zero when no known ages exist |
| `imaged_image_preparation_oldest_known_phase_age_seconds` | Current known age; zero for proven empty, absent for unknown-only phases or untrusted evidence |

Node routing matches recovery. Empty owners retain legacy compatibility and an
empty reader identity observes all owners. Scopes can overlap: compare instances
separately, never sum replicas. Labels contain no deployment/customer identifiers.

## Diagnose a prolonged phase

The warning uses 30 minutes for preparing/layer_published and 10 minutes for
scan_complete, continuously for five minutes with fresh evidence. These are
initial triage budgets, not execution timeouts or measured SLOs. Age includes
active work and waiting to resume behind serial work; healthy long operations
can warn. A two-second recovery ticker is not an execution budget.

| Phase | Work to inspect |
| --- | --- |
| `preparing` | Input acquisition, artifact lease, pulling/conversion, assembly and publication |
| `layer_published` | Replication, shared base staging and deployment scanning |
| `scan_complete` | Prepared-scan validation and snapshot/release-task handoff |

Use existing deployment stages/logs to identify work, then compare
`imaged_build_handoff_recovery_work_total{source="resume"}` and
`imaged_image_preparation_commits_total`. Repeated claimed commits do not prove
progress. Busy artifact leases are deferred and preserve age. Separate query
failures from handler errors, cancellations and protective token/state fences.
Check artifact ownership, database connectivity, replication and scanner health
according to phase. Do not delete inputs, defeat leases or force phases/tokens.

Actual committed phase changes start the next phase's clock. Retries and restarts
preserve it. Handed_off, live and terminal work leaves this set. Handed_off may
transfer work to a release task and does not mean serving traffic.

## Historical timing is unknown

The migration leaves existing timestamps NULL. Updated_at includes claims;
deployment creation and build completion cannot reconstruct current phase start
either. PostgreSQL stamps new inserts and actual phase changes, including older
writers during an upgrade. Same-phase writes preserve known timing or legacy NULL.

FaasImagePreparationPhaseAgeUnknown warns after 15 minutes of fresh observations
with such rows. Inspect durable phase, deployment state and recovery outcomes.
Mixed cohorts retain oldest KNOWN age and can also warn for a prolonged phase;
that known age need not be the oldest of the entire cohort. Never invent history
by touching timestamps, replaying claims or forcing phases. A legitimate phase
transition establishes timing for the next phase; until then retain unknown
evidence and use stages/logs.

See [build handoffs](FaasBuildImageHandoff.md) for succeeded builds without a
checkpoint, and [export retention](FaasBuildExportRetention.md) for input lifetime.
Existing two-hour stale-deployment recovery remains independent.

After handed_off, use [scheduler snapshot-prime diagnostics](FaasSnapshotPrime.md)
to distinguish release gates, waiting work, active capture and publication.
