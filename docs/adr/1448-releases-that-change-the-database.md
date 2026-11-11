# ADR-1448 · Releases that change the database

- **Status:** accepted (shipped in PR 4872, 2026-10-18); verified on n2 2026-10-15 (see Rollout)
- **Date:** 2026-10-15
- **Amends:** [ADR-1446](1446-safe-releases-by-default.md) (rollback targets), [ADR-122](122-safe-releases.md) (rollback)
- **Follows:** the release phase (release commands run as app tasks before a candidate boots), managed PostgreSQL migration bindings and restore-to-new-database
- **Decision:** A rollback whose target has not served since a later release command ran is refused with `409 rollback_crosses_release` unless the caller acknowledges it. The problem lists each such release and, for every managed PostgreSQL database the app's release commands can migrate, the point in time just before that release, as a ready `gregale postgres restore` command. Automated rollbacks never acknowledge. Each deployment that has a release command reports it as `release` (status, start and finish times, migratable databases).
- **Why:** A release command, typically a migration, must succeed before the new version serves, and then nothing tracks it. Rollback restores old code, never the database. A rollback to the release that was serving when the migration ran is safe enough: that code already ran on the migrated schema for the whole rollout. But a rollback further back runs code on a schema it has never seen, and nothing says so. The restore needed to undo the migration exists (managed PostgreSQL restores any point in its window), but a customer would have to work out the timestamp themselves, under pressure.
- **Rejected alternatives:** Taking a provider snapshot before every release command (Neon's point-in-time restore already covers the window, and a provider call per release adds a failure mode to the release path). Blocking every rollback after any migration (the common rollback, back to the release that served during the migration, is safe and must stay one command). Undoing migrations automatically (Gregale cannot know whether a migration is reversible; restoring a database is a customer decision). Recording a release flag on the deployment row (the release task already records status and times, so a second copy could drift).

## Context

A deployment with a release command (`release:` in a Procfile, or `release.command` in the manifest) gets one release app task. imaged admits it after the image is built. The candidate is primed only after the task succeeds, so the serving release keeps all traffic while the migration runs, and keeps serving on the migrated schema during the canary. Release tasks alone receive managed PostgreSQL migration credentials (`--access migration` bindings). A failed release task leaves its committed database changes in place.

Rollbacks have three entry points: `POST /v1/apps/{slug}/rollback` (CLI and API; plain, or checked with `expected_current_deployment_id`), the dashboard, and the first-wake 5xx rollback worker. The safe-release circuit breaker aborts a canary by restoring the predecessor that is still serving, so it never moves to an older release.

## Decision

### Which rollbacks cross a release

A deployment's `serving_ended_at` is when it last stopped serving traffic. It is cleared while the deployment serves again. A rollback to target T crosses every release task of another deployment in T's scope that started after T last served: after `serving_ended_at`, or after T was created if T never served. Any started task counts, including failed ones, because a failed migration may still have committed changes. T's code has never run against what those tasks did.

The release that replaced T started while T was serving, so the usual rollback (to the previous release, by the first-wake 5xx worker or `gregale rollback`) crosses nothing.

### The guard

`rollbackAppCore` and the checked rollback path resolve the target as before, then compute the crossed releases. If there are any and the request does not set `acknowledge_releases: true`, APID answers `409 rollback_crosses_release`. The problem's `crossed_releases` lists each release (deployment ID, revision, task status, start time) and `restore_points` lists, for every database the app's release commands can migrate in that scope, the start time of the earliest crossed release and the command that restores the database to that moment as a new database. An acknowledged rollback proceeds and records the crossed releases in its audit row.

The first-wake 5xx worker, alert-rule rollback actions sent by meterd, and the dashboard never acknowledge. A 5xx rollback that would cross a release stops: the release keeps serving and the worker logs the skip on each sweep until the 5-minute first window closes. The dashboard shows the problem and points to the CLI.

The migratable databases are the managed PostgreSQL databases whose migration binding delivers a secret into the deployment's scope, the same rows a release task receives. A database attached after a release is listed too: the restore command still names a time before that release.

### Restore points, not checkpoints

Managed PostgreSQL restores any point inside its restore window into a new database, without touching the source or its bindings. The restore point is the release task's `started_at`. Gregale does not call the provider before a release. A restore point outside the window fails at restore time with the provider's window error.

### Reporting releases

`DeploymentResponse.release` is set for a deployment that declares a release command: task status, `started_at`, `finished_at`, and the migratable databases. `gregale deployment summary` prints it.

## Consequences

- A rollback further back than the previous release now needs one more step, with a prompt in the CLI or `--acknowledge-releases`. The common rollback is unchanged.
- An automated rollback never runs code on a schema it has not served with; the release that triggered it keeps serving and a person decides.
- Customers get the exact restore command for a managed database instead of reconstructing a timestamp. Databases outside Gregale get the release start time and nothing more.
- Old code still runs on the migrated schema during every rollout, as it did before this ADR. The guidance remains expand-then-contract migrations; this ADR does not change the release phase.

## Rollout

Ship the guard, the problem fields, the CLI prompt and the `release` field together. apidserver tests cover the crossing rule (the release that replaced the target is not crossed, an older target is, and the refusal leaves the target untouched), acknowledgement and its audit row, the checked path, and the `release` field. The store tests cover the crossing query on MemStore and Postgres (a failed release counts; another scope, the target's own release and releases before it do not; the cut-off moves to `serving_ended_at` once the target served) and the migratable-database query on Postgres. The CLI tests cover the explanation, `--acknowledge-releases`, and a script getting the refusal back.

Verified on the n2 node on 2026-10-15: the apidserver rollback tests (new and existing, including the checked, dashboard, alert-historical and 5xx paths), the state, CLI, unit and spec tests pass. The full safe-release drill passed with leakcheck clean; its manual rollback goes through the guard and recovered in 10.4 s.

## Amendment 1 (2026-11-05): release commands run one at a time, and not after a lost one

Release review round 16 found two gaps in how release commands run.

**Overlapping migrations.** A deployment had at most one release task, but nothing ordered the release tasks of different deployments. While v2's migration ran (a backfill, say), v3 could finish building and schedd could claim v3's migration alongside it, or, when v2's task returned to the queue, after it. Two migrations against one database, or an older one after a newer one, can leave a schema neither release expects. The outpaced-release check (ADR-20261020085706 amendment 13) only looked at releases whose command had not been admitted.

- The claim (`ClaimNextUnfencedAppTask`, and the MemStore mirror) takes a release task only when no other release task of the same app and scope is restoring or running, and no older one is queued. Scopes are independent; manual tasks are unchanged.
- `SupersedeOutpacedRelease` now also retires a release whose command is still queued: the task is cancelled (status guard `queued`, so a claim that already took it wins) and the release superseded in one transaction. imaged calls it for the older releases of the scope (the newest 50 deployments) each time it admits a release command, so a queued older migration never runs before a newer one. A command already claimed keeps its outcome; the go-live fence supersedes its release as before.

**A lost migration.** A release command whose worker lease expired after dispatch is failed and never replayed (ADR-230), and so is one that timed out or was cancelled while running. It may have committed part of its changes. The next release ran its own migration on top without anyone looking. Project promotions already block new migrations while an outcome is uncertain; single apps now do too.

- A release command is *uncertain* when it started (`started_at` set), ended `failed`, `timed_out` or `cancelled` without an exit code, nobody checked it, and no later release command of the scope succeeded.
- imaged fails a release before admitting its command when its scope has an uncertain one: `release_outcome_uncertain`, naming the lost release and the command to record a check. The claim refuses such a command too, which covers one admitted before the earlier outcome became uncertain; it waits until the check, or until the stale sweeps close its release.
- `POST /v1/deployments/{id}/release-outcome` (`deploy:write`, MFA, org action `org.deploy_app`) records the check in `release_outcome_checks` with the caller and a required note; `gregale release resolve <id|vN> --note TEXT` calls it. It answers `409` when the release's command never ran, ended with an exit code, or was checked already. The check is audited as `app.release_outcome_checked`.
- A release command that failed with an exit code is not uncertain: its output says what happened. The rollback guard above already counts it as crossed.

Tests: conformance cases `release_commands_run_one_at_a_time_per_scope` and `outpaced_queued_release_command_is_cancelled` (MemStore and Postgres); imaged tests for the retirement and the uncertain refusal; apidserver and CLI tests for the check.

## Amendment (2026-11-10): platform failures of the release task are not the command's

A release task that schedd could not run or observe (`restore_failed`,
`restore_timeout`, `execution_transport_failed`, `guest_protocol_error`,
`attach_unroutable`) failed its deployment as `release_command_failed`, telling
the customer their command failed; production recorded two on 2026-11-05
("app task environment could not be prepared"). imaged now fails such a
deployment with `release_infrastructure_failed` and whycopy guidance to deploy
again. A non-zero exit, a command timeout, a cancellation and refusals tied to
the account or a database cutover keep `release_command_failed`. The previous
release keeps serving in every case. Validation:
`TestReleaseTaskFailureNamesWhoFailed`.

## Amendment (2026-11-10): a lost worker lease is an uncertain outcome

A release task whose worker lease expired after dispatch (`lease_expired`,
recorded by schedd's sweep) still failed its deployment as
`release_command_failed`, although the command may have finished and
amendment 1 already treats that outcome as uncertain. imaged now fails it as
`release_infrastructure_failed`, and whycopy says what is different from the
other platform failures: the outcome is unknown, and the next release command
waits until `gregale release resolve` records a check. Validation:
`TestReleaseTaskLeaseExpiredIsUncertainNotCommandFailure`.
