# ADR-1451 · Guarded, reversible config changes

- **Status:** accepted (shipped in PR 4872, 2026-10-18); verified on n2 2026-10-17 (safe-release drill config-guard case)
- **Date:** 2026-10-17
- **Amends:** [ADR-211](211-effective-environment-state.md) (customer-secret versions), [ADR-625](625-first-wake-5xx-rollback-from-request-telemetry.md) (first-wake rollback, now also for config)
- **Follows:** [ADR-1446](1446-safe-releases-by-default.md) and [ADR-1447](1447-explained-rollout-decisions.md): releases are health-gated and explained; config changes were neither
- **Decision:** Every customer change to an app's environment variables or secrets records a config revision, and `gregale env history` / `gregale env rollback <revision>` list and restore them. A change also opens a config guard. The guard judges the first ten minutes of traffic served with the new config against the same app's traffic before the change. If the new config clearly fails, the guard restores the previous config, restarts the app and records why. Secret values are never kept beyond an open guard, and a deleted or rotated secret is never restored automatically.
- **Why:** A release gets a canary, a health gate and an automatic rollback; a config change gets none of them. `PUT /v1/apps/{slug}/env/{key}` and `PUT …/secrets/{key}` overwrite the value in place (`ON CONFLICT … DO UPDATE`). They invalidate the scope's snapshots, and the next wakes cold-boot with the new value. A wrong `DATABASE_URL` takes every new instance down, nothing notices, and the old value is gone, so the fix depends on someone remembering it.
- **Rejected alternatives:**
  - **A config canary now**, using a config-only deployment that reuses the build and splits traffic. Config is resolved per app and scope, not per deployment (`ReadRuntimeAppEnvForDeployment`). Snapshot fences, secret delivery, runtime reload and revocation all assume that. Pinning config per deployment is stage 2 (below), not a prerequisite for catching the common failure.
  - **Keeping old secret values indefinitely for undo.** That would keep credentials a customer believes are replaced. Retention is bounded by the guard.
  - **Restoring a deleted or rotated secret automatically.** Deletion is revocation (ADR-339), and rotation is the security action. Undoing either must be a person's decision.
  - **Restoring part of a guarded change.** A mixed config that never existed is worse than the failing one.

## Context

Customer config is `app_envs` (plain values) and `app_secrets` (sealed ciphertext, `kid`, `value_hash`, `secret_version`), keyed by `(app_id, scope, key)`. A guest receives the variables and secrets of its deployment's scope at boot from vmmd. A process environment is part of a snapshot's memory, so each write invalidates the scope's snapshots and stamps `app_runtime_config_changes` (ADR-352). Running instances keep their old values until they park, unless the customer restarts the app. Each request to the API changes one key; `gregale env push` sends one request per key.

The gateway records every proxied request, including failed wakes, in `request_telemetry`, with `instance_id` and `received_at`. An instance started after a config change runs the new config, because snapshots taken before the change are stale and the ADR-352 fence refuses to publish a capture of an older process.

## Decision

### Config revisions

After each customer write to a scope's variables or secrets, apid records a revision in `app_config_revisions`. A revision is per app and scope and carries:

- a monotonic `revision` number, actor and time;
- its source: `env.set`, `env.delete`, `secret.set`, `secret.delete`, `secret.rotate`, `config.rollback` or `config.guard_restore`;
- the key and action;
- the scope's complete variable set after the change;
- each secret's `secret_version` and `value_hash` after the change, but never its ciphertext.

The first write to a scope that has no revision first records a `baseline` revision of the state before the write, so the first change can be undone too. apid keeps the newest `api.ConfigRevisionsRetained` (100) revisions per app and scope.

Revisions are recorded after the write, in their own transaction, under a per-scope advisory lock. Each revision holds the complete state, so a missed record (logged and audited) leaves a gap in the history but never a wrong later revision. Managed credentials (managed PostgreSQL, object storage) and environment GitOps writes are not customer config writes here. They keep their own rotation and ownership rules, and they never open a guard.

### Rollback

`POST /v1/apps/{slug}/config/rollback?scope=` with `{revision}` (CLI `gregale env rollback <revision> [--scope]`) sets and deletes variables until the scope matches the target revision. It goes through the same write path, so snapshots are invalidated and the rollback is itself a guarded change. Secrets cannot be restored from a revision, because a revision holds no values. The response lists every secret whose version differs from the target as `secrets_not_restored`, with both versions. `GET /v1/apps/{slug}/config/revisions` lists the scope's revisions, newest first, each with its guard's status.

### The guard

A write opens a guard for its app and scope. If a guard is already open for that scope, the write joins it, so the several keys of one `env push` are judged and restored together. The guard records:

- the base revision, from before its first change;
- for each secret the guard replaced, the previous sealed row (`ciphertext`, `kid`, `value_hash`, `secret_class`);
- for each secret the guard added, that it did not exist.

A guard opens only when the scope has a live deployment. Configuring an app before its first deploy has no traffic to judge, and its first release is judged by its own rollout. A write with `?guard=false` (CLI `--no-guard`) opens no guard. If a guard is already open, the write closes it as `superseded`, because its traffic could no longer be attributed to one config.

An apid worker sweeps open guards every 15 seconds. For the deployments in the guard's scope that existed when it opened, it compares two samples. A release deployed during the guard is judged by its own rollout and never blamed on the config.

- **New config:** requests received after the guard's latest change that were served by an instance started after that change, or that failed before an instance served them. Instances that started between two changes of one guard ran a config that is no longer current, so they count in neither sample. Telemetry is bucketed by minute, so the minute of the change counts only through its instances.
- **Old config:** requests to the same deployments in the hour before the guard opened.

The window opens on the first new-config request and lasts `api.ConfigGuardWindowMinutes` (10). A scale-to-zero app may stay quiet for hours, so a guard that sees no new-config traffic within `api.ConfigGuardMaxWait` (24 h) closes as `no_traffic`.

The new config fails when either holds:
- its responses cross the ADR-625 first-wake bar: at least `RollbackOn5xxMinServerErrors` (5) 5xx, which are also at least `RollbackOn5xxMinErrorPct` (50%) of its requests;
- both samples meet the circuit breaker's request floor (20) and the canary's 5xx regression rule holds (`canary.ServerErrorRegression`, the same rule that aborts a canary).

A failed wake is recorded as a gateway 5xx, so an app that crashes at startup with the new config fails the guard on its first few requests.

When the new config fails and every change in the guard can be restored, apid:
1. restores the base revision's variables;
2. writes back each replaced secret's sealed row and deletes each secret the guard added. Nothing is unsealed, so no plaintext is involved.
3. invalidates the scope's snapshots and requests a runtime-config restart of the app, so instances that are running with the failed config are replaced;
4. records a `config.guard_restore` revision and closes the guard as `restored`.

If the guard deleted or rotated a secret, it closes as `regressed` instead and restores nothing. Both outcomes keep the decision on the guard: rule, both samples and a one-line summary, in the shape of ADR-1447. They emit an audit row (`config.guard_restored` or `config.guard_regressed`), like the first-wake rollback worker.

A guard whose window passes without failure closes as `clean`. Closing a guard for any reason erases the secret rows it retained.

### Surfaces

- `gregale secrets set` and `gregale env push` print one line after the write when a guard is watching it, pointing to `gregale env history`. `--no-guard` on those commands, `gregale secrets unset` and `gregale env rollback` opts out. Env and secret write responses carry the revision and guard as `config_change`.
- `gregale env history [--scope]` lists revisions with the changed key, actor, source and guard verdict. `gregale env rollback <revision>` restores one.
- `GET /v1/apps/{slug}/config/guard?scope=` returns the scope's newest guard and its decision.

### Stage 2: config canaries (follow-up)

A config change would create a config-only deployment that reuses the live deployment's build and pins a config revision. The safe-release canary would then split traffic between the two configs, as it does between two builds. That needs config resolved per deployment (`ReadRuntimeAppEnvForDeployment`, the ADR-352 fence, secret delivery, reload and revocation), and it is a separate ADR.

## Consequences

- A config change that breaks the app is reverted automatically within about a minute of the first failing traffic, plus the restart. Quiet apps are judged on their first wake after the change.
- Every config change can be undone with one command, as far as variables go. Secrets are restorable only while their guard is open.
- A replaced secret's previous sealed value is retained for the guard's life, at most `ConfigGuardMaxWait` plus one window, then erased.
- The guard judges 5xx only. A config change that slows the app down, or breaks one rarely used route, is not caught. Latency and probe evidence can be added once config canaries exist.
- `app_config_revisions`, `app_config_guards` and `app_config_guard_secrets` are new tables. They belong to apid as customer intent and are registered in the clone schema registry.

## Rollout

Ship the revisions, the guard, the API and the CLI together. The guard is on by default. Tests cover:
- revision recording and retention on both stores;
- rollback of variables, and the report of secrets it cannot restore;
- the guard's decision on both samples;
- restore of variables and replaced secrets, and the `regressed` path for a deleted secret;
- the CLI.

A metal test on n2 deploys the hello fixture, sets a variable that makes it answer 500, and checks that the guard restores the previous value and the app serves again.
