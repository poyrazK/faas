# ADR-20261023181931 · Rollbacks that restore the database

- **Status:** proposed; shipped in PR 5305 (2026-10-25) behind the preview `postgres-restore` flag. Acceptance needs an end-to-end drill with a real provider restore; the n2 fixture providers do not restore. Amendment 1 (2026-10-26)
- **Date:** 2026-10-23
- **Amends:** [ADR-1448](1448-releases-that-change-the-database.md) (rollbacks over release commands), [ADR-1333](1333-managed-postgres-activation-preflight.md) and [ADR-1335](1335-managed-postgres-source-writer-isolation.md) (activation stays disabled for every cutover except this one), [ADR-464](464-managed-postgres-cutover-preparation.md), [ADR-466](466-managed-postgres-cutover-admission-fence.md) and [ADR-1153](1153-managed-postgres-durable-drain-plans.md) (cancellation is no longer the only way to release pins and the admission fence)
- **Decision:** An acknowledged rollback may set `restore_databases`. Gregale then switches the app's managed PostgreSQL database back with the code. It restores the database to just before the earliest release the rollback crosses, into a new database. It stages and verifies the app's credentials for that copy, and fences the app so nothing new starts. It drains the app's running instances and makes the rollback target the live release. In one transaction it then replaces the app's bindings to the source database with bindings to the copy. Last, it invalidates the app's snapshots and lifts the fence. Writes made after the restore point stay in the source database, which Gregale does not change. `gregale rollback --restore-databases` runs it and reports each phase.
- **Why:** ADR-1448 refuses a rollback that crosses a release command and prints the restore command for each managed database. The person must then run the restore, wait for it, move the bindings by hand and roll back. A binding can only be replaced by deleting it and attaching another, so the app runs without credentials for a while, and code and schema stay mismatched until both steps finish. The cutover machinery (ADRs 464–467, 993, 1153, 1333–1335) already stages credentials, fences admission and drains instances with native receipts. What it lacks is the step that switches the app's bindings to the copy, which every one of those ADRs left disabled.
- **Rejected alternatives:**
  - **Restoring the copy and leaving the switch to a person.** This is what ADR-1448 already prints, minus one command. The dangerous part is the window in which old code runs against the migrated schema, or new code against the restored one, and only an orchestrated switch closes it.
  - **Restoring in place.** The provider-neutral contract (ADR-155) restores only into a new database. An in-place restore also destroys the only copy of the writes made after the restore point.
  - **Waiting for ADR-1335's provider writer-isolation evidence.** Neon cannot give it, so the switch would never run. That evidence protects a cutover that must not lose writes. A rollback switch loses the writes after the restore point by design, and the person acknowledges that loss. A writer that escapes the fence can only write to the source, which the app no longer uses (see "The rollback bar").
  - **Checking the target's readiness before the switch.** A readiness boot needs admission, and the fence exists to prevent admission. The target is an attested release that served before; its first wake after the switch is its readiness check.
  - **Enabling activation for every cutover.** A plain cutover keeps the source's writes and must prove that every writer stopped. That bar stays as ADR-1333 wrote it; only this switch activates.

## Context

A release command (`release:`), typically a migration, runs with the managed PostgreSQL migration binding before the new release takes traffic. ADR-1448 lists, for a rollback target T, the release commands of other deployments in T's scope that started after T last served. T's code never ran against their changes. The restore point is the start of the earliest of them, truncated to the second.

The cutover chain stops before activation:
- **Staging and verification (ADR-464/465):** each source binding of an app and scope gets a new binding ID and sealed credentials on the target, verified by SQL.
- **Admission fence (ADR-466/467):** an app-wide fence stops instance admission, task claims and boots. Only a cancelled cutover can lift it.
- **Drain (ADR-993/1153):** schedd retires the app's captured instances and records native receipts.
- **Activation preflight (ADR-1333):** reports what blocks activation, and `activation_allowed` is always false.

## Decision

### The request

`POST /v1/apps/{slug}/rollback` accepts `restore_databases: true` together with `acknowledge_releases: true`. Without acknowledgement, the rollback is refused with ADR-1448's `rollback_crosses_release` as before; the CLI shows that refusal as the plan before asking for confirmation. In addition to the usual rollback checks, the switch requires:
- the rollback crosses at least one release command;
- the target's scope has exactly one database its release commands can migrate (ADR-1448's migratable databases). More databases are refused for now: one cutover fences the app;
- the token has managed PostgreSQL manage scope, besides deploy write scope;
- managed PostgreSQL provisioning is open for the account;
- the scope's traffic is not split, and no other switch or cutover is active for the app;
- the request is not a checked rollback (`expected_current_deployment_id`). The switch performs its own check: it records the live release at request time and aborts if another release serves before the switch.

The restored database is named `<source>-before-v<revision>`. When a database with that name exists, the switch reuses it if it is a restore of the same source at the same point, and refuses otherwise. A person who already ran ADR-1448's suggested restore therefore does not pay for a second copy.

The request answers 202 with the switch. The code rollback does not start yet. Automated rollbacks (first-wake 5xx, post-release watch, alert actions, dashboard) never set `restore_databases`.

### Phases

apid runs one worker for all switches. A lease on the row keeps two apid processes off the same switch, and every step can be retried after a crash.

| Phase | What happens | Leaves when |
|---|---|---|
| `restoring` | Starts the restore (ADR-1259 proves the copy's lineage). | The copy is ready, or `DatabaseSwitchRestoreTimeout` passes. |
| `staging` | Prepares the cutover for the app and scope, source to copy. | Every credential is sealed. |
| `verifying` | Requests SQL verification. | Every credential is verified. |
| `fencing` | Installs the admission fence. The trigger requires verification less than five minutes old; stale evidence returns to `verifying`. | The fence is in place. The app stops serving from here. |
| `settling` | Waits for members the drain cannot retire to finish: undispatched wakes, running tasks, migrations. | None are left, or `DatabaseSwitchSettleTimeout` passes. |
| `draining` | Asks schedd to capture the drain plan. schedd's capture loop reserves it and runs drain recovery at once. | Every member is retired or parked, or `DatabaseSwitchDrainTimeout` passes. |
| `switching` | Re-verifies credentials if the evidence is older than five minutes. Checks that the live release is still the one recorded at request time, then makes the target live (`PrepareDeploymentRollback`, then `MarkDeploymentLive`). Then it runs the activation transaction. | The transaction commits. |
| `resuming` | Invalidates the app's snapshots, lifts the fence and notifies the gateway and schedd, then revokes the source bindings' provider credentials. | The credentials are revoked; the switch is `completed`. The app serves from the moment the fence lifts. |

Before the activation transaction commits, any failure aborts the switch:
1. If the target was already made live, the release that served before is made live again.
2. The cutover is cancelled. Cancellation revokes the staged credentials, then releases the pins and the fence (ADR-464).
3. The switch ends `failed` with a stable code, or `cancelled` when a person asked for it.

The app then serves the release it served before, on the source database. The restored copy is kept; deleting it is the person's call. `POST /v1/apps/{slug}/database-switches/{id}/cancel` is accepted until the switch reaches `switching`.

### The activation transaction

The transaction takes locks in the existing order: account, app, cutover, databases by ID, bindings by ID. It requires:
- the app is fenced by this cutover, at the fence time the switch recorded;
- the cutover is verified, and every member's verification is at most five minutes old;
- the drain plan for this fence epoch has every member retired with its native receipt, or parked. The plan's only acceptable blocker is `inventory_empty`;
- the target release is the scope's live deployment.

It then:
- marks the cutover `activated`, and its credentials `published` (their staged envelopes are erased);
- releases the cutover's pins on both databases and on the source bindings;
- deletes the source bindings and their app secrets. The copy's bindings take the same keys in the same transaction, and only one undeleted binding may hold a key, so the source bindings cannot wait in `deleting`. They keep their provider identities, and `resuming` revokes those credentials after the fence lifts, retrying until the provider confirms;
- inserts the target bindings with the IDs reserved at staging, generation 1 and state `ready`, and publishes their sealed credentials to `app_secrets` under the same keys;
- stamps the app's runtime configuration change.

The triggers that guard the admission fence, database pins and binding pins accept `activated` as well as `cancelled` as the state that releases them. Activation is one-way. Only the switch worker activates; no cutover API does, and preflight still reports `activation_disabled`.

### The rollback bar

ADR-1333 and ADR-1335 require proof that every writer of the source has stopped, because a plain cutover must not lose writes. A rollback switch discards the writes after the restore point, and the person acknowledges that before it starts. What it must guarantee is that the app does not serve from two databases, or serve a release against a schema that release never ran with. The guarantees are:
- **Admission is fenced:** no instance, task or boot starts between the fence and resume (ADR-466/467).
- **Every captured instance is retired with a native receipt (ADR-993):**
  - a parked instance has no process (invariant 4), and its snapshot is invalidated before resume;
  - a member the drain cannot retire (frozen, undispatched, migrating, a qualification instance, a task) aborts the switch.
- **The gateway routes only to instances in the catalog,** and after resume the catalog holds only instances of the target release booted with the copy's credentials.

A process that escaped the catalog, a standalone job holding a copied connection string, or a client outside Gregale can still reach the source. Its writes land in the database the app has left, which is the same loss the person already acknowledged for the source after the restore point. The switch does not close the source's connections (ADR-1334); the source stays usable for comparing and copying data.

### After the switch

- The first wake cold-boots the target release with the restored credentials. The app is unavailable from the fence until that wake serves. All scopes of the app pause, because the fence is app-wide.
- A rollback to a deployment whose release command the switch undid is refused with `rollback_target_database_restored`. That code cannot be acknowledged, so automated rollbacks cannot return to it either. Running that code again means deploying it, which runs its release command.
- ADR-1448's crossing check leaves out releases a completed switch undid. A later rollback lists only the release commands still in effect.
- The source database keeps its data and its other bindings. Its bindings for this app and scope are removed. Deleting it is the person's call.

### Limits and reporting

`pkg/api/limits.go` holds the restore (60 minutes), settle (2 minutes) and drain (10 minutes) timeouts and the worker cadence (5 seconds).

`GET /v1/apps/{slug}/database-switches` and `GET /v1/apps/{slug}/database-switches/{id}` report:
- the phase, the restore point and both databases;
- the releases the switch undid and the drain counts;
- the failure code and the phase timestamps.

Deployment audit rows record the request, the switch and the outcome.

## Consequences

- A rollback over a migration can now undo the migration with one confirmed command, at the cost of downtime measured in tens of seconds plus a cold boot, and the loss of writes after the restore point.
- Only one migratable database per scope is supported. Apps with frozen instances or long-running tasks at fence time fail the switch safely, and keep serving the current release.
- The managed PostgreSQL provider's restore window bounds how far back a switch can go. A restore point outside it fails in `restoring`, before anything else changes.
- Plain cutovers remain unactivatable; ADR-1333's bar for them is unchanged.

## Rollout

The switch ships with the migration that adds `release_database_switches`, the new cutover and credential states, and the amended triggers. It needs managed PostgreSQL provisioning, which stays closed in production until opened per account. Tests cover:
- **State and managed PostgreSQL stores:** the switch rows on MemStore and Postgres; activation and its refusals on Postgres (stale verification, drain blockers, a changed live release, pins, the fence, secrets and bindings swapped).
- **The worker, with fakes:** every phase, the aborts before and after the target goes live, cancellation, and lease handling.
- **apid:** the request checks, the switch routes and the rollback guard.
- **schedd:** the capture loop.
- **CLI:** the plan, confirmation and progress output.

## Amendment 1 (2026-10-26): the preview flag gates the request too

`postgres-restore` (preview) owns the `database-switches` routes, but `restore_databases` on `POST /v1/apps/{slug}/rollback`, a GA route, started a switch for every account. While the capability is off for the caller, the field now answers `400` as the unknown field it is to them, like its hidden routes. The status is back to proposed until an end-to-end drill restores a real provider database.

Operations: the worker reports each tick and each retried step (`faas_release_worker_*{worker="database_switches"}`), and each outcome (`faas_release_guard_decisions_total{guard="database_switch"}`). `FaasDatabaseSwitchAborting` and `FaasDatabaseSwitchFailed` alert. A failed switch has already undone itself: the previous release serves on the source database, and the restored copy is kept. See `docs/runbooks/FaasReleaseGuards.md`.
