# ADR-20261102182856: Build logs are kept 90 days, except for live and recent deployments, and capped per build

- **Status:** accepted
- **Date:** 2026-11-02
- **Amends:** ADR-011 (thin dashboard, which added `deployment_logs` in M7.5)

## Context

builderd inserts every build line into `deployment_logs` (migration 00006,
M7.5, ADR-011) and nothing deletes the rows: a deployment keeps its build log for as
long as the app exists. `Config.BuildLogMaxBytes` (8 MiB by default) bounds only
the node-local log file; the database copy has no bound. The DPA (section 4)
tells customers logs are deleted 90 days after they are written, and the
2026-10-31 logs audit (R3) found build logs outside that promise.

Erasure already works: rows cascade from `deployments`, and app purge and
account erasure delete deployments.

## Decision

1. **Horizon.** meterd deletes a deployment's `deployment_logs` rows once the
   build's last line (highest `seq`) is older than
   `meter.BuildLogRetentionDays`. That constant is
   `MaxLogEventRetentionDays`, the longest customer log window (Scale's
   `LogArchiveRetentionDaysMax`, 90 days, which the archive bucket's lifecycle
   rule also enforces), so build and runtime output share one horizon. Keying on
   the last line keeps a build's log whole.
2. **Exceptions.** A deployment keeps its build log while it is live
   (`status = 'live'`: the serving release, a canary or traffic split beside it,
   a release staged with `--no-traffic`) or is among its app's
   `api.BuildLogRetainedDeployments` (10) newest deployments by `created_at`,
   whatever their status. This keeps rollback targets and
   `gregale logs --latest-failure` working. The count is per app and wider
   than `SnapshotRollbackRetentionDeployments` (3), which counts only live and
   superseded releases of one scope.
3. **Sweep.** meterd owns it, beside the `log_events`, request telemetry and
   `deployment_audit` sweeps (deleting rows is not a write to customer intent;
   builderd stays the only writer of lines). It runs at boot and hourly. Each
   pass takes one sixteenth of the deployment id space (first hex digit, chosen
   by the UTC hour), so every build is examined at least every 16 hours however
   often meterd restarts. A statement examines at most 500 builds with rows and
   deletes at most 10,000 rows by primary key; a pass runs at most 1,000
   statements. Every step is an index probe: a loose index scan over
   `deployment_logs_pkey` visits each build that still has rows once (not each
   line), the last line comes from `deployment_logs_seq_idx`, the newest
   deployments from `deployments_app_created_id_idx`. No index is added. An
   index on `written_at` was rejected: the retained builds' old lines would sit
   at its head and be rescanned on every batch.
   Metrics: `meterd_build_log_retention_deleted_total`,
   `meterd_build_log_retention_last_success_timestamp_seconds`,
   `meterd_build_log_retention_failures_total`.
4. **Byte cap.** builderd stores at most `Config.BuildLogMaxBytes` of a
   deployment's build output. The line that would cross the cap is replaced by
   one marker row (stream `system`, "build log truncated …"); later lines are
   neither stored nor sent to live subscribers. The running count is kept in
   memory per deployment and re-read from the database
   (`Store.DeploymentLogUsage`: summed bytes and whether the marker exists) on
   a deployment's first line in the process and after a minute without a line,
   so a restart mid-build or a build requeued to another builder continues the
   stored count and writes no second marker. The node-local file keeps its
   silent cap.
5. **Readers.** `GET /v1/deployments/{id}/logs` sends `event: expired` with
   `{"message","retention_days","retained_deployments"}` after an empty first
   page when the deployment was built, is not live, was created before the
   horizon and is not among its app's newest. `gregale release logs`,
   `gregale logs --latest-failure` and `gregale logs --deployment` print the
   message. Older clients ignore the frame and show an empty log, as before.

## Consequences

- `deployment_logs` stops growing without bound; a fleet's retained build logs
  are about 90 days of builds plus up to 10 deployments and the live releases
  per app.
- A rollback to a release more than 10 deployments back and older than 90 days
  works but has no build log.
- A pass costs one probe per build with rows in its slice; after the first
  passes that is the last 90 days of builds plus the retained ones.
- The database copy of a noisy build ends at the cap with a marker; the
  deployment's error and failure hint are unaffected.
- The dashboard does not yet render `event: expired`; it shows an empty log.
