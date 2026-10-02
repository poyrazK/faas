# Managed PostgreSQL operator preview

> Managed PostgreSQL remains gated to qualified accounts while the provider
> qualification is completed. Its provider-neutral commands are nevertheless
> exposed through the customer-facing `gregale` CLI so eligible workloads use
> the same workflow in development and production.

Managed PostgreSQL now has a customer-facing, provider-neutral API while
remaining an opt-in operator preview. The API exposes account-scoped database
CRUD/status, restore-to-new-database, and workload bindings under
`/v1/postgres/*`. Provider IDs, passwords, connection URLs, and secret
ciphertext never cross that API boundary. `provisioning_enabled` must remain
false until an isolated provider qualification is approved; reads and durable
deletion reconciliation remain safe while provisioning is disabled.

Plan entitlements are enforced before provider work and again at the atomic
database reservation boundary:

| Plan | Databases | Storage | Restore window | Classes |
| --- | ---: | ---: | ---: | --- |
| Free | 0 | — | — | — |
| Hobby | 1 | 10 GiB | 7 days | development |
| Pro | 3 | 50 GiB | 7 days | development, burstable |
| Scale | 10 | 100 GiB | 7 days | development, burstable, production |

All current plans use provider-managed compute suspension. Always-on database
compute is not included in the bundled plans, and requests that disable
scale-to-zero are rejected until a separately priced entitlement exists.

The operator registry can set a lower global database or provider ceiling;
customer limits never raise it. API keys use `postgres:read` for read/status
operations and `postgres:manage` for create, restore, delete, and binding
operations. Binding credentials are delivered through the existing app-secret
surface after the binding saga reaches `ready`.

Keep `provisioning_enabled` false outside an isolated provider qualification
environment. The lifecycle service and background discovery also require all
of the following runtime gates before they will provision: `FAAS_ENVIRONMENT`
must be `staging`, `FAAS_MANAGED_POSTGRES_QUALIFIED=true`,
`FAAS_MANAGED_POSTGRES_QUALIFIED_VERSION=3`,
`FAAS_MANAGED_POSTGRES_QUALIFIED_UNTIL` must be a future RFC3339 timestamp,
and the exact qualified backend ID and fingerprint must be supplied through
`FAAS_MANAGED_POSTGRES_QUALIFIED_BACKEND` and
`FAAS_MANAGED_POSTGRES_QUALIFIED_FINGERPRINT`. A single default backend is
required during this preview. Deletion intents are still reconciled, so
disabling rollout cannot strand known paid resources.

For a narrower first rollout, set `FAAS_MANAGED_POSTGRES_CANARY_ACCOUNTS` to a
comma-separated list of exact account IDs. Database creation, restore, and
binding provisioning then run only for those staging accounts; an empty value
keeps all qualified staging accounts eligible. Empty entries, entries longer
than 255 characters, or lists larger than 100 accounts fail closed. Reads and
all deletion/revocation paths remain available outside the allowlist so an
operator can drain a canary safely before expanding it.

## Provider health

`gregale postgres get <database>` and database GET/list responses include cached
`health`: `healthy`, `degraded`, `stale`, `unknown`, or `disabled`. Lifecycle
`state` stays separate. A provider outage or deleted upstream resource does
not turn a ready catalog row into a provisioning request.

Health reads provider metadata only. Suspended compute is healthy when the
resource is ready and its configuration matches. A healthy signal does not
prove SQL connectivity; use `gregale bindings verify <app> --postgres DATABASE_URL`
for the existing application connection check. Monitoring does not wake compute,
retrieve credentials, repair roles, or recreate resources. API GET/list never
contact the provider.

The optional registry policy is:

```json
"health": {
  "enabled": true,
  "interval_seconds": 60,
  "stale_after_seconds": 300
}
```

These are the defaults when the policy is omitted. Collection continues for
existing ready databases with provisioning disabled. Set `enabled` to false to
opt out; the API then reports disabled and suppresses retained observations.
Intervals must be 60–3600 seconds; freshness must be 120–86400 seconds and at
least twice the interval. Restart `apid` after changing the configuration.
Apply the health migration before starting the updated binaries.

`checked_at` is the latest attempt, including provider API failures. `fresh`
means that attempt is recent. `last_success_at` is the latest valid metadata
response, which may itself describe a degraded resource. `last_error_code`
is sanitized; provider IDs and credentials stay private. Without an observation,
health is unknown. Non-ready databases are not polled; mutation responses may
omit health until the next GET/list.

Collectors share durable per-database leases across replicas. Each process
checks at most 20 rows per sweep with one-second spacing and a ten-second
provider timeout. Provider request failures back off to fifteen minutes, so a
persistent outage can become stale. Replicas do not share a global provider
rate limit. Monitor stale counts and sweep age when growing the catalog; do
not interpret cached observations as a guaranteed real-time availability SLO.
See [ADR-389](adr/389-managed-postgres-provider-health.md) and the
[health runbook](runbooks/FaasManagedPostgresDegraded.md).

## Provider qualification

Qualification is a separate operator action and is never started by `apid`.
Build and run `cmd/managed-postgres-qualify` only against an isolated staging
organization. Set `FAAS_ENVIRONMENT=staging`,
`FAAS_MANAGED_POSTGRES_QUALIFY_LIVE=true`,
`FAAS_MANAGED_POSTGRES_QUALIFY_RESOURCE_ID` to a disposable logical identity,
and point `FAAS_MANAGED_POSTGRES_CONFIG` at the provider configuration. The
command provisions one resource, retries the same idempotency key to exercise
ambiguous-create recovery, inspects it, reads a complete usage window, issues
and revokes a read/write credential, and deletes the resource. When the
qualification spec advertises a point-in-time restore window, it also writes
a marker in the disposable source, captures a database-clock timestamp after
that commit, changes the marker, and restores to the captured point. It waits
for target readiness, issues a target credential, and verifies the earlier
marker through PostgreSQL before revoking that credential and deleting the
target. The source fixture is removed before its owner credential is revoked.
It waits for asynchronous deletion to complete and always
attempts cleanup after an intermediate failure and emits a JSON report with
only stable check codes and restore evidence (without provider IDs). The
command also emits a versioned `approval`
envelope, an `approval_env` block when all rollout checks pass, and a
machine-readable `readiness` result. Version 3 requires SQL permission probes,
data recovery, and rejection of inherited source logins on the restore target.
Versions 1 and 2 must be replaced by a new qualification run.
The approval is bound to the report digest, exact backend fingerprint, expiry,
and the current canary allowlist. A provider-only run remains useful evidence
but is not rollout-ready until the lifecycle smoke has passed.

Save the JSON output as an operator-owned artifact and verify it without making
provider calls:

```sh
FAAS_ENVIRONMENT=staging \
FAAS_MANAGED_POSTGRES_CONFIG=/etc/faas/managed-postgres.json \
FAAS_MANAGED_POSTGRES_CANARY_ACCOUNTS=acct_demo \
FAAS_MANAGED_POSTGRES_QUALIFY_APPROVAL_PATH=/var/lib/faas/managed-postgres-qualification.json \
go run ./cmd/managed-postgres-qualify --verify
```

Verification compares the artifact with the configured single default
backend, its non-secret placement fingerprint, the provider-neutral spec, and
the current canary allowlist. It exits non-zero with stable blocking reasons
when the approval is missing, expired, tampered with, or not lifecycle
qualified. When the approval path is configured on `apid`, the same artifact
is loaded at startup and is authoritative for staging provisioning; invalid,
expired, tampered, or mismatched artifacts keep the gate closed. The legacy
`FAAS_MANAGED_POSTGRES_QUALIFIED*` variables are only a fallback when no
approval path is configured. Restart `apid` after replacing the artifact.
Provisioning remains disabled until the operator deliberately enables it.

Set `FAAS_MANAGED_POSTGRES_QUALIFY_LIFECYCLE=true` for the second,
control-plane smoke in the same isolated run. After the provider checks pass,
the command uses the provider-neutral service and binding saga to exercise
`database_create → database_ready → binding_create → binding_ready →
binding_delete → database_delete`. The smoke uses an in-memory catalog and a
non-persistent credential sink, so it validates lease transitions, provider
credential issuance/revocation, and cleanup without writing a customer app
secret or exposing a password. This flag is also staging-only and remains
independent of the customer provisioning gate.

The binding catalog, credential saga, and encrypted-secret ownership boundary
are durable. Reserving a binding claims one `(app, scope, environment key)`
target globally, so two databases cannot both own `DATABASE_URL`. A customer
secret write racing that reservation is serialized in PostgreSQL; exactly one
wins. Once claimed, customer PUT, rotate, and DELETE operations return a
conflict until the binding reaches its deleted tombstone. Host-key maintenance
can still re-seal the ciphertext without changing the managed owner.

The binding reconciler issues a deterministic provider identity, builds a
percent-encoded PostgreSQL URL, and seals it under the host age recipient before
marking the binding ready. The catalog stores only the provider's opaque
identity ID and an opaque deterministic secret reference. If `apid` stops after
the provider call or secret write, the expired lease is rediscovered and the
same identity and secret reference are retried. Deletion revokes the upstream
identity first, removes the owned secret, and only then writes the binding
tombstone.

Binding provisioning follows `provisioning_enabled`; binding deletion runs even
while the flag is false. A missing host age recipient or HMAC key leaves the
binding failed with a retry rather than storing plaintext or marking it ready.

Rotate a binding with `POST /v1/postgres/bindings/{id}/rotate`. The operation
creates the next deterministic provider identity and replaces the sealed app
secret, which invalidates old runtime snapshots. It keeps the previous provider
identity valid until the scheduler completes the rolling runtime refresh, then
the binding reconciler revokes that identity with retry-safe cleanup. The
response sets `rotation_pending` while the previous identity is still retained;
retries during that interval reuse the same generation and wake ID. If the app
has no live deployment or resident instances, the previous identity is queued
for cleanup immediately.

## Neon backend configuration

Copy `deploy/managed-postgres.example.json` to an operator-owned path, set
`FAAS_MANAGED_POSTGRES_CONFIG` to that path, and expose the API key through the
environment variable named by `secret_env.api-key`. The API key itself must
never appear in the JSON file.

`namespace` is the immutable Neon organization ID. `region` is Gregale's
portable region name; `settings.region_id` is the current Neon placement for
that region. Existing database rows contain a fingerprint of this non-secret
placement. Changing the organization, physical region, or limits fences those
rows instead of silently moving provider operations to a new destination.

The adapter requires explicit `max_storage_bytes` and
`max_restore_window_seconds` settings. Set them to promises supported by the
Neon organization plan. A value of `0` disables the point-in-time-restore
promise. The storage ceiling must be positive.

The initial service-class mapping is:

| Gregale class | Neon autoscaling range |
| --- | ---: |
| `development` | 0.25–1 CU |
| `burstable` | 0.25–2 CU |
| `production` | 1–4 CU |

Only `single_zone` is advertised. Gregale does not claim a portable
high-availability promise merely because Neon storage has internal redundancy.
`scale_to_zero=true` uses a 300-second suspend timeout; false disables compute
suspension. Customer requests cannot currently select `false`; the adapter
retains the provider-neutral field for a future always-on entitlement.
PostgreSQL majors 14 through 18 are enabled.

The mutating qualification run also proves the runtime behavior: it connects
to the disposable database, closes the connection, waits for Neon to report
the endpoint as idle, reconnects, records wake latency, and only then allows
the backend fingerprint to be approved. Configuration inspection alone is not
sufficient evidence for a scale-to-zero promise.

## Safety boundary

Neon's create-project operation is non-idempotent. The adapter never retries
that POST at the HTTP layer. It assigns a deterministic, hashed project name,
discovers that exact name before creation, and returns the Neon project ID as
soon as the POST is accepted so the durable reconciler can use `Inspect` from
then on. If the create response is lost, it performs discovery without issuing
a second POST. Deletion also searches by Gregale's stable resource ID when the
opaque Neon ID was never persisted, closing the ambiguous-create cleanup path.
Ambiguous duplicate names fail closed.

Before any provider delete call, the PostgreSQL catalog takes an exclusive row
lock and atomically rejects databases with an active binding or restore
descendant. Binding reservations take a key-share lock on their target database,
and restore reservations take the same lock on their source. Consequently, a
concurrent reservation either commits first and blocks deletion, or observes the
database in `deleting` and fails without creating a dependent. The provider is
never contacted after a dependency conflict.

Each binding uses a deterministic SQL-created Neon login with a random password.
Repeating issuance verifies its privileges and recovers the stored password
without resetting it. The control plane keeps `gregale_owner` credentials
private; they never reach the app-secret sink. Provider errors contain stable
codes without response bodies, connection strings, endpoint hosts, or API keys.

| Binding access | Connection | Permissions |
| --- | --- | --- |
| `read_write` (default) | Pooled, with direct fallback | Public-schema SELECT, INSERT, UPDATE, DELETE; sequence usage; RLS enforced |
| `migration` | Direct required; release tasks only | Schema changes through a stable non-login schema owner; no role/database administration or replication |
| `read_only` | Read-only endpoint required | Not advertised by the current Neon adapter |

Runtime logins cannot create tables, temporary objects, schemas, or roles,
truncate tables, or bypass row-level security. Runtime grants cover existing
public tables/sequences and future objects created by the schema owner.
Custom schemas and function execution require explicit migration-owner grants.
Existing PUBLIC-executable SECURITY DEFINER functions block credential issuance;
review their execution grants before adoption. New owner functions do not
receive PUBLIC execution privileges by default.

Use a separate environment key for migration tooling:

```sh
gregale postgres attach DATABASE APP_SLUG --access migration --env MIGRATION_DATABASE_URL
```

Managed migration bindings are injected only into the persisted release task.
Serving instances, companions, ordinary manual tasks, and cron tasks cannot
receive them through default delivery, an explicit secret reference, or runtime
secret reload. Access is read from the binding catalog, so changing an environment
key cannot change the delivery boundary. Release tasks receive migration bindings
even when a serving `env_secrets` allowlist selects only runtime credentials.
A plain customer secret with the same name has ordinary secret semantics.

Use a Procfile `release: npm run migrate` or `release.command` in the manifest.
Release execution requires `FAAS_RELEASE_PHASE_ENABLED=1` on imaged and
`FAAS_APP_TASK_DISPATCH=1` on schedd. A disabled release gate fails the candidate
with `release_phase_unavailable` instead of skipping its migration.
The existing release gate waits for successful completion before booting the
candidate. Failure leaves the preceding deployment live; it does not roll back
committed database changes. The PostgreSQL starters use direct
`MIGRATION_DATABASE_URL`, advisory transaction locks, a 30-second lock timeout,
and a 120-second statement timeout. Serving startup performs no DDL. For other
frameworks, declare your migration tool as the release command and configure it
to use the migration connection.

Migration sessions automatically switch to the schema owner. `SET ROLE NONE`
removes DDL access until that owner is selected again, so normal migrations do
not assign ownership to a disposable login. Both runtime and migration
credentials rotate through the existing binding workflow. Migration rotation
publishes the new generation without restarting serving workloads; old-login
retirement waits for all restoring/running app tasks in that app and scope to
finish. Queued tasks load the new generation when dispatched. Retirement disables
login and terminates sessions before deleting grants and the role. It refuses
to delete a role that owns objects, preserving application data for operator
repair. Stable schema objects survive migration credential retirement.

Existing preview administrator bindings require deliberate rotation to adopt
restricted credentials. Existing table ownership is preserved; an operator may
need to transfer application objects to the stable schema owner before migration
credentials can alter them. A restored branch disables inherited managed logins
before reporting ready; target bindings receive branch-scoped identities.

Gregale checks backend-supported access before reserving a binding and again
during reconciliation. Provider-supplied root-certificate PEM fails closed
until the binding contract can deliver a separate sealed certificate file.
The new privilege and Neon SQL password-recovery behavior require live version
3 qualification before enabling a staging canary.

Neon's consumption-history API maps compute and network transfer directly to
Gregale's `compute_unit_seconds` and `egress_bytes` meters. Neon reports root
and child branch storage, instant-restore history, and snapshot storage as
byte-hours under billing-oriented `*_bytes_month` names. The adapter converts
those values to Gregale's canonical `storage_byte_seconds` and
`history_byte_seconds` meters only after summing each complete provider window;
it rejects arithmetic overflow rather than recording a wrapped quantity.

## Usage collection and admission guardrails

The provider-neutral usage ledger is disabled by default. An operator may turn
it on with the `usage` block in the example configuration, after replacing the
zero-valued caps and rates with approved commercial limits. The collector
imports only complete provider windows and records them idempotently by
database, window, and meter. Enabled policies require monthly caps for compute,
storage, and egress; history is optional because providers may not expose
point-in-time or snapshot storage. Rates use integer millicents per CU-hour,
GiB-hour of storage/history, or GiB of egress.

`managed_postgres_usage_coverage` commits a contiguous completed-window
checkpoint atomically with each window's meter readings. Collection begins in
the window containing database creation and resumes from that checkpoint after
an outage or process restart. Each sweep backfills at most 24 windows per
database; remaining backlog is deferred to the next sweep. Missing advertised
meters or a failed window do not advance coverage. Once caught up, the latest
completed window is refreshed so newer provider corrections replace its values;
older observations cannot overwrite newer readings. Historical corrections
outside that latest window still require explicit reconciliation.

The migration does not infer coverage from old ledger rows, because those rows
may contain gaps. Existing databases replay from creation, replacing identical
window keys without increasing totals. Admission stays stale until recovery
reaches the latest completed policy window for every ready database. Provider
history that is no longer available must be reconciled by an operator; it is
never silently skipped. Keep `usage.window_seconds` unchanged for databases
with recorded usage: changing its duration fails closed to prevent overlapping
windows from counting consumption twice and requires an accounting migration.
Deleting a database retains its recorded consumption in monthly account totals.

When enabled, a new database reservation is admitted only if the account has a
complete, fresh usage coverage for each ready database and has not crossed its
monthly cost, compute, storage, history (when configured), or egress ceiling. Missing or stale observations fail
closed; an existing named database remains idempotent and can still be
reconciled. The plan's per-database storage entitlement is multiplied by the
plan's database-count allowance, converted to the canonical byte-second meter,
and intersected with the operator ceiling; compute, history,
egress, and spend remain operator-configured COGS ceilings. This keeps the
admission path provider-neutral and prevents a plan or provider adapter from
raising the operator's safety budget.

The billing seam emits stable internal line-item codes
(`managed_postgres.compute`, `managed_postgres.storage`,
`managed_postgres.restore_history`, and `managed_postgres.egress`) with
normalized quantities and provider-cost millicents. They are COGS/invoice
mapping inputs only: current plans are bundled and do not expose provider
costs or surprise overage charges to customers. A future invoice adapter can
attach the codes to the included managed-postgres product without changing
the Neon adapter or the customer API.

Customers can inspect the current account snapshot with
`GET /v1/account/managed-postgres-usage` (the `usage:read` scope) or
`gregale postgres usage`. The response reports normalized compute,
storage/history, and egress meters, ready-database count, the bundled storage
headroom, and a `guardrail_state` of `disabled`, `healthy`, `stale`, or
`reached`. `fresh=false` is informational for this read surface; admission
continues to fail closed when observations are stale. Provider IDs, provider
rates, internal cost line items, credentials, and connection URLs never appear
in the customer response.

Operators with the admin scope and MFA can inspect the same account through
`GET /v1/admin/managed-postgres/usage/{account_id}`. This bounded view adds the
effective normalized safety ceilings and internal millicent COGS line items;
it still omits provider resource IDs, credentials, and connection URLs.

Account deletion is fail-closed until all managed databases reach their
provider-confirmed `deleted` tombstone. The final GDPR deletion transaction
then removes usage, binding, and database tombstones before deleting apps and
the account, so a stale catalog row cannot block deletion or remain after the
account is gone. Lifecycle actions emit audit events without credentials,
connection strings, or provider IDs.

The adapter contract is based on Neon's maintained
[v2 OpenAPI specification](https://neon.com/api_spec/release/v2.json). A live
qualification run against an isolated organization is still required before
enabling provisioning.

## Restore-to-new-database safety

The control plane exposes restore as a new logical database intent. A restore
requires a ready source, a point in time inside its configured restore window,
and a new target name. The source database and its bindings are never changed;
the target receives its own provider resource and can be bound explicitly
after it is ready. The source database ID, provider resource identity, and
requested timestamp are written before provider I/O so an expired worker lease
can resume the same restore instead of creating a second target.

Neon's adapter implements this using a deterministic point-in-time branch in
the source project. The opaque target ID is encoded inside the adapter as
`project_id/branch_id`; the provider-neutral catalog never interprets that
format. Credentials, inspection, usage, and deletion route to the branch.
Neon reports consumption at project scope rather than per branch. With usage
guardrails enabled, Gregale therefore collects that project aggregate once
against the source database and skips direct usage collection for every restore
descendant. Descendants share the root source's coverage; a source with missing
windows keeps its targets stale. This keeps account-level COGS ceilings effective without claiming
per-target usage isolation; the usage collector exposes skipped descendants as
`included_in_source` rather than as independently metered databases. Providers
that can meter restores independently may instead declare that capability.
If a create response is lost, branch-name discovery recovers the accepted
branch without a second POST. Deleting a source is rejected while an active
restore descendant exists, and deleting a restore target removes only its
branch. Cutover remains an explicit binding operation; restore never silently
rewires an app.

## Restore cutover preparation and verification

After restoring to a ready target, stage every runtime and migration binding
for one app and scope, then request SQL verification:

```sh
gregale postgres cutover prepare orders orders-copy api --scope production
gregale postgres cutover get CUTOVER_ID
gregale postgres cutover verify CUTOVER_ID
gregale postgres cutover get CUTOVER_ID
```

Preparation and verification are asynchronous. `prepared` means target
credentials are privately sealed. `verified` means the control plane successfully
authenticated each staged credential and checked database identity, PostgreSQL
version, and role ACLs in a read-only transaction. `verification_fresh` requires
every member's evidence to be no older than five minutes. Request `verify` again
to refresh it. These checks do not prove application VM reachability, data
correctness, or application compatibility.

Workloads keep using the source throughout this workflow. The current API has
no activation operation; scheduler writer draining and atomic publication remain
pending. Both databases and the selected source bindings stay pinned until
`gregale postgres cutover cancel CUTOVER_ID` finishes revoking every staged role.
Cancellation and status remain available when provisioning is disabled. Required
host age/HMAC keys must remain available while envelopes are staged; cancel and
reprepare before retiring those keys. See [ADR-391](adr/391-managed-postgres-cutover-verification.md).

The next cutover prerequisite is an internal, durable app-wide admission fence
([ADR-392](adr/392-managed-postgres-cutover-admission-fence.md)). It requires fresh
verification and blocks new instance admission and running-state publication,
including worker, job, mirror, and warm-pool paths. It persists through retries
and credential verification refresh, and completed cancellation releases it.
Release, manual and command-cron task claims and command dispatch also honor this
barrier. Queued tasks wait, subject to their usual start deadlines; a task fenced
during restoration is destroyed before command dispatch and records
`database_cutover_fenced`. VM destruction now cancels
and waits for boots already inside vmmd, including snapshot restores and task VMs.
On nodes configured with the authoritative control-plane DB URL, vmmd also
checks this durable fence inside the registered boot flight before allocating
resources. Delayed boot RPCs must read the fence again, including after a daemon
restart. Warm and migration resume/capture operations use the same admission check
and destruction join. Default-local nodes honor a configured `db_url` or
`FAAS_VMMD_DBURL`; DB-less nodes cannot participate in cutover drains.
See [ADR-394](adr/394-managed-postgres-vmmd-admission.md). Teardown now retains
live and failed-boot ownership until process exit and cleanup are confirmed;
concurrent stops wait, failed cleanup blocks reuse, and Destroy can retry the
original resources ([ADR-395](adr/395-managed-postgres-confirmed-teardown.md)).
Scheduler watchdog, liveness, OOM, operator restart and pressure-recycling paths
also retain their resident state, admission and host-port ownership when Destroy
fails; a schedd restart rebuilds those reservations. Read failures and state races
cannot free another operation's resident capacity
([ADR-396](adr/396-managed-postgres-scheduler-teardown-accounting.md)). Remaining
boot/migration cleanup paths need further work. Liveness and workload OOM reports
now persist on vmmd and retry failed delivery/cleanup; schedd distinguishes
acceptance from applied outcomes, including across-node relays. Source node
binding refuses reports delayed past a completed migration
([ADR-397](adr/397-managed-postgres-failure-report-redelivery.md)).
This proof is local to the running daemon. Crash recovery, all-node
capability/ownership checks and durable drain receipts remain pending.
Operators must keep `/var/lib/faas/vmmd-failures` on persistent storage. Pending
report logs include instance ID, kind, attempt and retry delay; a growing backlog,
unsupported RPC, missing durable row or persistence error needs investigation.
Committed reports survive process exit. A recovered report with unknown Manager
ownership stays pending until guest inventory is reconciled; do not delete the
spool or treat a cold instance row as fleet teardown proof.

Linux vmmd now inventories surviving Firecracker/jailer processes, links,
namespaces and jails before allocating prepared networks or serving RPCs. It
quarantines observed allocator slots and rejects boot/stop/resume requests for
their instance IDs ([ADR-398](adr/398-managed-postgres-restart-resource-quarantine.md)).
Startup logs report the observed slot, instance and process counts. An incomplete
required inventory fails startup. Quarantine persists for that Manager's lifetime;
even a later orphan sweep does not release those slots. Reattachment, durable
resource cleanup and resumed report delivery remain pending. Investigate through
the owning vmmd and scheduler; an observed quarantine is not a drain receipt.
The internal nested-node [diagnostics](ops/evidence/20261002-managed-postgres-restart-quarantine/README.md)
cover real guests with UUID, builder-prefixed and compact IDs; supported native
lifecycle acceptance remains pending.

Prepare and Verify never install this fence. Existing VMs and SQL sessions still
require scheduler drain; atomic publication and customer activation remain
unavailable. Lifecycle acceptance requires native x86_64 KVM tests and leakcheck.

## Customer usability

The `gregale postgres` command is the supported customer entry point for the
provider-neutral API:

```sh
gregale postgres list
gregale postgres usage
gregale postgres create orders --region eu --class development
gregale postgres get DATABASE_ID
gregale postgres restore DATABASE_ID --name orders-copy --point-in-time 2026-09-09T10:00:00Z
gregale postgres bindings create DATABASE_ID --app APP_ID --scope production --environment-key DATABASE_URL
gregale postgres bindings list DATABASE_ID
gregale postgres bindings rotate BINDING_ID --wait
gregale postgres attach orders api --scope production --env DATABASE_URL
gregale postgres delete DATABASE_ID
```

Binding rotation returns as soon as the new credential is active. Add
`--wait` to poll until `rotation_pending` clears; the wait defaults to five
minutes with one-second polling. Set `--wait-timeout` or `--poll-interval` to
adjust those limits. If the timeout expires, the command prints the latest
binding state and exits with status 1.

For the App Platform-style happy path, `gregale add postgres` composes the
same lifecycle and binding APIs. It reuses a matching account database when
one exists, otherwise creates one, waits for readiness, and binds the sealed
`DATABASE_URL` value to the app environment. The command never prints the
credential or connection URL:

```sh
gregale add postgres --app api --env production --region eu
gregale add postgres orders --app api --env production --region eu
gregale add postgres --database orders --app api --env production
```

`--database` accepts either the database ID or logical name. `NAME` is
optional when creating and defaults to `<app>-postgres`; `--region` is only
required when a new database must be created. The existing `gregale postgres`
verbs remain available for lower-level lifecycle and restore operations.

For application deployments, declare the dependency in `gregale.yaml` and
`gregale deploy` creates the durable binding before uploading compute:

```yaml
databases:
  - database: orders       # logical database name or ID
    scope: production      # defaults to default
    env: DATABASE_URL      # defaults to DATABASE_URL
    access: read_write     # read_only only when the selected backend supports it
```

The `app` field can be supplied for a multi-app project manifest; when it is
omitted, the declaration applies to the app currently being deployed. For a
project deploy with multiple selected workloads, `app` is required so each
binding has an unambiguous target. Project deploys and immutable GitHub
source-ref deploys perform the same server-side resolution and binding step as
the single-app CLI path, before compute is queued. The CLI resolves the
database name/ID, requires a ready binding response, and then relies on the
existing sealed app-secret injection path. A binding that is still provisioning
blocks the deployment with a retryable error; credentials are never written to
the manifest or printed by the CLI. All dependencies for one deployment must
use the same scope; an explicit `--environment` selects that scope and rejects
conflicting manifest scopes so compute and the sealed database secret resolve
together.

Pass `--json` to any read or write command for automation. JSON responses use
the same DTOs as the public API and deliberately contain no password, endpoint,
connection URL, or secret ciphertext. Human output shows lifecycle state,
placement, storage/restore allowances, and binding generation/state so a
customer can tell whether a workload is ready without opening provider
consoles.

The signed-in dashboard exposes the same read-only view at
`/dashboard/postgres`. It is safe to bookmark during a rollout: when the
managed-Postgres service is disabled or temporarily unavailable, the page
shows an explicit status message rather than implying that an empty catalog is
healthy. Database creation, restore, deletion, and binding changes stay on the
CLI/API surface, where the existing authentication, plan, idempotency, and
provider-neutral validation rules apply.

## Cutover preparation foundation

The internal `CutoverService` can reserve preparation for every source binding
in an app and scope, stage encrypted target credentials, and cancel preparation
with retry-safe provider revocation. The binding reconciler resumes persisted
work after crashes and performs cancellation with provisioning disabled.

A `prepared` intent has sealed credentials for runtime and migration access;
it has not verified SQL reachability or switched application configuration.
Staged envelopes remain outside `app_secrets`. This slice exposes no public
cutover endpoint or CLI command. The next implementation must drain affected
writers, validate the target, publish all bindings together, invalidate snapshots,
and restart/verify workloads before allowing traffic again.

Source and restored target databases stay pinned while preparation is active.
Conflicting rotation, deletion, and new binding reservations return a conflict.
Cancellation revokes every possibly issued identity before erasing staging and
releasing pins. Recovery cancels intents belonging to already-deleted owners;
the existing app/account deletion guards still require live resources to be
cleaned up first. Cancel
all active intents before migration rollback or retiring a host age identity;
the existing secret re-sealer does not cover these staged envelopes.
See [ADR-390](adr/390-managed-postgres-cutover-preparation.md).
