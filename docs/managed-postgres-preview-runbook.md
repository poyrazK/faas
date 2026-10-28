# Managed PostgreSQL preview runbook

This runbook covers the dark/isolated preview. Customer provisioning remains
disabled until the staging qualification evidence is reviewed and the exact
backend fingerprint is approved.

## Qualification approval

Before spending provider credits, run the no-side-effect configuration
preflight. It loads the operator configuration, validates the single default
backend and placement fingerprint, checks the provider-neutral qualification
spec against every paid plan, and validates the staging/resource/canary
inputs. It never calls Neon, creates a database, or emits an approval:

The secret environment named by the backend's `secret_env.api-key` mapping
must still be present so the adapter can validate its configuration; the
preflight reads it but never sends it to Neon.

```sh
FAAS_ENVIRONMENT=staging \
FAAS_MANAGED_POSTGRES_CONFIG=/etc/faas/managed-postgres.json \
FAAS_MANAGED_POSTGRES_QUALIFY_RESOURCE_ID=qualification-preflight \
go run ./cmd/managed-postgres-qualify --check-config
```

The JSON result contains stable check codes and a `readiness` object. Warnings
such as `usage_policy_disabled` or `restore_usage_unaccounted` do not make the
provider call safe to skip; they remain launch blockers that must be resolved
or explicitly documented before a staging canary. `restore_usage_not_isolated`
documents that branch-level usage is not available. A provider that declares
`RestoreUsageIncludedInSource` may nevertheless support guarded restores
without per-target metering, provided its source usage response accounts for
all descendants.

Run the provider qualification with
`FAAS_MANAGED_POSTGRES_QUALIFY_LIFECYCLE=true` and
`FAAS_MANAGED_POSTGRES_QUALIFY_DURABLE=true`, using the fixture below, and save its JSON output in an
operator-owned path. The output includes a versioned approval envelope and the
exact `approval_env` values for the staging gate. Verify the saved artifact
before applying those values:

A newly created provider project may have no usage in the previous completed
hour. To continue independent credential, compute, restore and deletion tests,
set `FAAS_MANAGED_POSTGRES_QUALIFY_CONTINUE_AFTER_USAGE_FAILURE=true` for that
isolated run. The usage failure remains in the report, the command exits
non-zero, and it emits no approval or provisioning gate values. This option
does not relax any other prerequisite. A complete settled usage window is
still required for qualification and rollout. Cleanup failures are reported
alongside the original failure so operators can recover every leaked resource.

The Neon restore fixture chooses a whole-second database clock boundary after
the first committed marker and waits for that boundary before writing the
second marker. It never rounds a customer restore request. Incomplete branch
creation acknowledgements are resolved by bounded reads of the acknowledged
branch identity, with exact source and timestamp validation before adoption
or cleanup. Fractional timestamps that Neon cannot report exactly remain
unqualified; timestamp tolerance is not a lineage proof.

Live PostgreSQL 18 tests on 2026-10-06 observed a whole-second restore
request whose ready branch reported the earlier WAL commit's timestamp.
ADR-677 adds independent historical-source WAL verification for that case;
[the recovery evidence](ops/evidence/20261007-managed-postgres-recovery/REPORT.md)
records normal, restarted, and lost-response recovery. Waiting for metadata or
rounding the test point still cannot establish a timestamp-to-LSN mapping.
Snapshot creation acknowledgements can omit both timestamp and expiry; read
the accepted snapshot until its complete metadata is available before adopting
it or changing retention. Missing or conflicting final metadata remains a
blocker. These diagnostics do not authorize enabling the production service.

Snapshot capture replay and lost-response discovery use the same bounded reads
as fresh creation. Missing metadata can settle; a reported conflicting owner,
source or capture point stops recovery even when other fields are missing.
Private snapshot restore also hydrates only the original target branch, including
temporary absence after acknowledgement. It independently reads readiness and
never finalizes the preview or substitutes a matching display name for a known
target identity. An unavailable observation remains retryable; it is not proof
that the provider-side resource was never created.

Restore discovery, replay and cleanup read all bounded branch pages before
creating a target or declaring it absent. Discovery pins creation-time ordering
and rejects missing lists, overlapping identities, duplicate owner names and
cursor cycles. An incomplete provider listing cannot authorize another create
or report cleanup complete.
Live local Neon tests on 2026-10-07 observed `sort_order: "ASC"` in responses
to the lowercase `asc` query. Inventory validation accepts ascending ordering
regardless of casing; descending ordering on any page still stops discovery,
replay and cleanup before mutation.
Lifecycle inspection, read-only health observation and default-source selection
use the same complete branch inventory, so a later-page branch is not reported
missing. A missing or null branch list is unknown; an explicit empty list can
establish absence after pagination completes.

```sh
FAAS_ENVIRONMENT=staging \
FAAS_MANAGED_POSTGRES_CONFIG=/etc/faas/managed-postgres.json \
FAAS_MANAGED_POSTGRES_QUALIFY_APPROVAL_PATH=/var/lib/faas/managed-postgres-qualification.json \
go run ./cmd/managed-postgres-qualify --verify
```

Verification is read-only: it checks the report digest, expiry, lifecycle
checks, provider-neutral spec, exact configured backend fingerprint, and
canary allowlist without contacting Neon. A non-zero exit or any readiness
reason blocks rollout. Treat the artifact as expired when its `expires_at`
passes; rerun qualification instead of extending it by hand. Version 8
artifacts require runtime DML and RLS enforcement, denied DDL/administration,
stable migration ownership, and preserved data after migration login retirement.
When read-only access is advertised, approval also requires actual existing and
future table/sequence reads, denied mutations/DDL/administration and RLS bypass,
stable recovered passwords, rotation preserving data, and revoked old sessions
and fresh logins. A capability declaration alone is insufficient.
They also require a restore timestamp inside the disposable source's lifetime,
exact source and point lineage on creation and readiness, and same-target
restore replay. Earlier committed data, rejection of source credentials on
the target, and completed deletion. Version 8 also requires durable SQL restart
and encrypted credential delivery evidence. Versions 1–7 cannot authorize this release.

When `FAAS_MANAGED_POSTGRES_QUALIFY_APPROVAL_PATH` is configured on `apid`,
the provisioning gate loads that artifact at startup and validates it against
the configured backend and current canary list. The artifact is authoritative:
missing, malformed, stale, tampered, or mismatched approval keeps provisioning
disabled even if the legacy `FAAS_MANAGED_POSTGRES_QUALIFIED*` variables look
valid. Those variables are a fallback only when no approval path is set and
`FAAS_MANAGED_POSTGRES_QUALIFIED_VERSION=8` matches the current contract.
Unversioned environment approvals remain blocked.
Restart `apid` after replacing the artifact so the new document is loaded.

## Durable lifecycle fixture

Use a fresh run UUID and a private, disposable PostgreSQL catalog. Apply the
normal Gregale migrations and seed one test account and app using the existing
test-fixture or staging API tooling. The app must belong to the supplied
account. The qualifier neither runs migrations nor creates these intent rows.
Do not point it at a customer or production catalog.

Before running, mark **only this disposable database**, using its database
owner/admin connection. Substitute the actual database identifier and run UUID:

```sql
ALTER DATABASE qualification_catalog
  SET faas.qualification_run TO '11111111-1111-4111-8111-111111111111';
```

The qualifier reads the stored database-level setting directly; `SET`,
`set_config`, connection options and `PGOPTIONS` cannot bypass this guard.
Each reconstructed pool must still see the same marker and catalog identity.
Use a fresh UUID and catalog for each run; deleted lifecycle tombstones remain
for audit. Provisioning stays disabled in the configured deployment.

Create a disposable X25519 age identity and at least 32 bytes of random HMAC
key material in private regular files (0400 or 0600, no symlinks). Use the same
files throughout this run. Pass the private catalog connection URL through the
environment without putting it in reports or shell history. Add these inputs
to the existing staging/live provider qualification environment:

```sh
export FAAS_MANAGED_POSTGRES_QUALIFY_LIFECYCLE=true
export FAAS_MANAGED_POSTGRES_QUALIFY_DURABLE=true
export FAAS_MANAGED_POSTGRES_QUALIFY_RUN_ID=11111111-1111-4111-8111-111111111111
export FAAS_MANAGED_POSTGRES_QUALIFY_ACCOUNT_ID=22222222-2222-4222-8222-222222222222
export FAAS_MANAGED_POSTGRES_QUALIFY_APP_ID=33333333-3333-4333-8333-333333333333
export FAAS_MANAGED_POSTGRES_QUALIFY_AGE_IDENTITY_FILE=/private/qualification/identity.age
export FAAS_MANAGED_POSTGRES_QUALIFY_HMAC_KEY_FILE=/private/qualification/hmac.key
# FAAS_MANAGED_POSTGRES_QUALIFY_CATALOG_URL is supplied privately.
go run ./cmd/managed-postgres-qualify > /private/qualification/report.json
```

Catalog/key/ownership preflight happens before provider mutation. After the
provider probes pass, the durable stage uses the configured adapter with real
SQL and app-secret stores. It deliberately loses successful acknowledgements,
reconstructs the services, verifies credentials and SQL data through rotation,
then deletes bindings and the provider project with provisioning closed.
The fault disconnects the catalog before the saga can persist its error; each
restart waits for the unfinished lease to expire. Allow at least 20 minutes for
the durable stage (four default two-minute leases plus provider operations).
An independently reconstructed final session verifies tombstones and absence
of encrypted secrets. Failed cleanup blocks approval; recover retained resources
before dropping the catalog. Keep the sanitized report, then remove the
disposable catalog and private age/HMAC files. Revoke temporary provider keys
after checking the provider inventory for leaked resources.

Local acceptance uses:

```sh
FAAS_PGTEST_TEMPLATE_DATABASE=1 go test ./pkg/managedpostgres ./pkg/managedpostgres/credentialdelivery ./cmd/managed-postgres-qualify ./cmd/apid
```

Supply `DATABASE_URL` privately to an isolated PostgreSQL cluster with CREATEDB
and role administration for the test-only customer role fixture. The cluster
must enable TLS and password authentication for fixture roles. Without a
database URL, SQL integration tests skip and do not establish acceptance.
These tests simulate provider management; fresh live Neon v8 qualification is
still required. Guest injection, native egress, deployment and retirement of
old credentials require a disposable app canary on a supported native KVM host.

## Staging canary rollout

Keep the global qualification gates enabled only in the isolated staging
environment. To start with one or a small set of accounts, set
`FAAS_MANAGED_POSTGRES_CANARY_ACCOUNTS` to their exact account IDs separated by
commas. Verify a database create and binding smoke for each listed account,
then expand the list deliberately. Leave the variable empty only when every
qualified staging account is an intentional canary. The gate is fail-closed on
malformed entries and never blocks database deletion or credential revocation.

## Production rollout

Production does not use a qualification artifact
([ADR-20261028103115](adr/20261028103115-managed-postgres-production-provisioning.md)).
To open it:

1. In the managed PostgreSQL configuration, set `provisioning_enabled`, turn
   on `usage` with real prices and ceilings, and add
   `"fleet": {"max_databases": N, "max_monthly_cost_millicents": M}` sized to
   the provider budget.
2. With `FAAS_ENVIRONMENT=production`, run
   `managed-postgres-qualify --check-production`. It prints the readiness
   codes and the configured backend's ID and fingerprint, and never contacts
   the provider.
3. Set `FAAS_MANAGED_POSTGRES_PRODUCTION_BACKEND` and
   `FAAS_MANAGED_POSTGRES_PRODUCTION_FINGERPRINT` to those values,
   `FAAS_MANAGED_POSTGRES_PRODUCTION_APPROVED_AT` to now and
   `FAAS_MANAGED_POSTGRES_PRODUCTION_UNTIL` at most 90 days later. Rerun the
   check until it exits 0, then restart apid. apid logs the closed reasons at
   startup.
4. Release accounts with the `managed-postgres` capability flag: an account
   rule first, then plan rules or a ramp. Releasing an account is a flag
   change, not a deploy; the kill switch closes every account.
5. Renew the approval before `FaasManagedPostgresProductionApprovalExpiring`
   turns into `FaasManagedPostgresProductionApprovalExpired`. Changing the
   backend configuration changes its fingerprint and closes provisioning
   until step 3 is repeated.

`FaasManagedPostgresFleetNearCeiling` and `FaasManagedPostgresFleetPaused`
follow the fleet ceilings; see
[FaasManagedPostgresDegraded](runbooks/FaasManagedPostgresDegraded.md).

## Admission failures

- `managed_postgres_usage_stale`: stop new reservations, verify the provider
  usage API and the usage collector logs, then wait for a complete window. Do
  not bypass the gate by raising a plan limit. Deleting a database also holds
  the gate for its account until the final usage correction lands (the final
  window end plus three collection windows); `gregale postgres diagnostics`
  shows the deleted database's `final_correction` time. Requests for a region
  without a backend or an unsupported major fail as
  `managed_postgres_unsupported` before this gate.
- `managed_postgres_quota_exceeded`: inspect the account's plan allowance and
  the operator usage policy. A plan storage ceiling is an entitlement; the
  monthly compute, history, egress, and spend ceilings are COGS safeguards.
  Upgrade or reduce the workload rather than disabling metering.
- `managed_postgres_provisioning_not_enabled`: the rollout gate is closed for
  the account (provisioning flag, qualification or canary window, or the
  canary account allowlist). Nothing is down; existing databases and
  bindings keep working. Reopen the gate deliberately; an expired operator
  test window closes it without any other signal.
- `managed_postgres_new_databases_paused`: the fleet database cap or the
  monthly provider-spend breaker is reached for every account. Existing
  databases keep working. Check `apid_managed_postgres_fleet_*` against the
  configured ceilings and raise one deliberately, or wait: the database cap
  reopens as databases are deleted and the spend breaker at the next UTC
  month.
- `managed_postgres_unavailable`: keep provisioning disabled and use the
  lifecycle reconciler to finish deletion of known resources.

## Restore support

Restore is always restore-to-new-database. Confirm the source is `ready`, the
requested timestamp is inside the source restore window, and the provider
qualification report declares a supported restore-accounting mode: isolated
per-target usage or usage included in the source aggregate. Neon uses the
source-aggregate mode because consumption is project-scoped. The collector
records that aggregate once against the source and skips restore descendants,
marking them `included_in_source`; this supports aggregate COGS guardrails but
does not provide per-database restore cost attribution. Do not enable guarded
restores for a provider whose qualification reports neither accounting mode.

Neon may publish a parent WAL position while its timestamp is absent or refers
to an earlier rounded commit. ADR-677 requires a matching independently routed
historical source position before publishing the requested restore point.
Gregale pins the source branch and endpoint, uses verified TLS, rejects writable
or current-primary connections, and rechecks the exact target after reading the
position. A missing mapping keeps recovery unavailable; it cannot be replaced
with the request timestamp. Creation custody still permits safe compensation
when verification fails. Worker restarts repeat verification before readiness.

## Account deletion

An account deletion can include the account's personal managed databases
([ADR-20261024150500](adr/20261024150500-account-deletion-includes-managed-postgres.md)).
Personal means the ones the account created in its personal organization or in
an organization without resource ownership. Company-owned databases are never
included.

- **Non-payment deletion** always includes them, unless the operator sets
  `FAAS_NONPAYMENT_DELETION_MANAGED_POSTGRES=off` on apid and meterd. With the
  kill switch off, such an account stays suspended and counts in
  `meterd_dunning_deletion_blocked_accounts`
  ([`FaasDunningDeletionBlocked`](runbooks/FaasDunningDeletionBlocked.md)).
- **Self-service** includes them only when the customer asks
  (`include_managed_postgres_databases=true`, `--include-postgres-databases`, or
  the dashboard checkbox). Until launch, only accounts listed in
  `FAAS_ACCOUNT_DELETION_MANAGED_POSTGRES_ACCOUNTS` see the option. Without it,
  the deletion is refused with `409 account_deletion_managed_postgres_databases`
  until the customer deletes each database.

Nothing is deleted when the account enters `deleted_pending`, so an account
restored or paid back during the 30-day grace keeps its databases and loses the
opt-in. When the grace ends, apid's grace sweep deletes the bindings and then
the databases through the normal lifecycle. It retries every tick and logs
`grace: external cleanup deferred` until every database is a provider-confirmed
`deleted` tombstone. A database with a restore descendant waits for the
descendant, and one held by an environment clone waits for the hold.

The final deletion transaction then removes only deleted managed-Postgres
metadata and usage rows. It never erases an active provider resource as a
shortcut. Never delete the rows or provider projects by hand, and never disable
the guard trigger.

## Billing evidence

Monthly COGS evidence is the usage ledger plus the stable line-item mapping:

| Code | Canonical meter | Unit |
| --- | --- | --- |
| `managed_postgres.compute` | `compute_unit_seconds` | compute-unit seconds |
| `managed_postgres.storage` | `storage_byte_seconds` | byte seconds |
| `managed_postgres.restore_history` | `history_byte_seconds` | byte seconds |
| `managed_postgres.egress` | `egress_bytes` | bytes |

The line-item helper is deterministic and idempotent for a complete monthly
snapshot. Provider adapters may change without changing these codes.

## Observability

The apid metrics listener exposes low-cardinality lifecycle, binding, usage,
canary, and admission signals. Alerting for the preview is defined in the
Prometheus `managed_postgres` rule group. Use the
[`FaasManagedPostgresDegraded`](runbooks/FaasManagedPostgresDegraded.md)
runbook for reconciliation failures, deferred work, stale usage, and recovery
validation. The provisioning gate metric is informational: it is expected to
be zero outside an approved staging canary.

## Metering recovery after rollout

Apply the usage-coverage migration before starting the collector. Existing
ledger rows do not establish contiguous coverage: databases replay from their
creation window in batches of at most 24 windows per sweep. Preserve the
configured collection-window duration. Do not reset checkpoints or change
`usage.window_seconds` to bypass a stale guardrail.

Check `apid_managed_postgres_usage_collection_databases_total` outcomes and the account usage
API until each account returns `guardrail_state=healthy` (or `reached` when a
ceiling is exhausted). A provider error or missing meter defers the failing
window and keeps admission stale; later windows cannot conceal that gap.
Shared Neon restore targets use their root source's coverage and consumption,
so a stale source keeps its descendants stale without multiplying usage.
Recorded usage remains in account monthly totals after database deletion.
If provider history is outside its retention period, investigate and reconcile
that gap before enabling further reservations.

For `legacy_identity_unknown` diagnostics on deleted rows, retain and verify
resource ownership, the exact backend fingerprint, branch lineage, and actual
provider shutdown evidence. Preview with `gregale postgres reconcile ACCOUNT_ID
--file retained-shutdown.json --json`, review the boundary, then apply with its
`expected_revision` and a recently stepped-up operator session file. See the
[input format and evidence requirements](managed-postgres.md). Missing lookup
results and old logical deletion timestamps are insufficient.

The repair preserves the old catalog and coverage in an immutable receipt,
retains all monetary ledger rows, and resets derived coverage atomically. Run
normal collection or the retained-usage import against the accounting root,
then inspect account diagnostics again. Attaching an identity alone does not
settle missing windows, final corrections, budget headroom, or provider invoices.

## Credential privilege adoption

Apply `20261001105914375_managed_postgres_migration_credentials.sql` before
using `migration` bindings. Keep the staging provisioning gate closed until a
fresh version 8 live Neon qualification passes. Local PostgreSQL tests establish
SQL behavior; they do not establish Neon password recovery or branch isolation.

Version 7 replaces prior approvals, including version 6. The
[2026-10-07 live acceptance](ops/evidence/20261007-managed-postgres-qualification/REPORT.md)
passed the core provider and lifecycle contract for PostgreSQL 18. Snapshot
capture and native copy remain unqualified. Keep production provisioning closed;
review the exact artifact and staging canary before changing rollout gates. Inspect
`gregale postgres capabilities --json` before adoption: `read_only` is configured
support, while `provisioning_enabled` reflects the current rollout gate.
After qualification, attach a distinct `READ_DATABASE_URL` binding with
`--access read_only`. Reader permissions are enforced on primary pooled/direct
connections; no replica endpoint is required. Review PUBLIC, column, function
and future-object grants; later privileged migrations remain responsible for
preserving the reader boundary. Native SQL regressions cover inherited reader
login retirement during restore, but do not replace live branch qualification.

Rotate existing preview administrator bindings deliberately. The new runtime
login receives public-schema data access, while migrations use a separate direct
connection and the stable schema-owner role. Review existing object ownership
before adopting migration credentials; transfer only application objects, never
provider objects, and review SECURITY DEFINER execution grants. Existing object
ownership is not automatically reassigned.

A revocation conflict on owned objects disables the login and terminates its
sessions while preserving those objects. Repair ownership and retry cleanup;
never drop application data to unblock credential deletion. New migration
objects normally belong to the stable owner and survive rotation.

## Release-only migration credential rollout

Apply `20261001123539479_managed_postgres_release_delivery.sql` and deploy the
matching apid, schedd, and vmmd binaries across the complete fleet while new
managed provisioning remains gated. The migration invalidates snapshots for
apps with active migration bindings, stamps runtime configuration changes, and
resumes migration rotations staged by older binaries. The schema itself has no
new column. Rollback does not restore old snapshots or completed rotations.

Restart affected serving workloads after every participating binary is updated.
Enable `FAAS_RELEASE_PHASE_ENABLED=1` on imaged together with
`FAAS_APP_TASK_DISPATCH=1` on schedd for release-task acceptance. Disabled release
execution must fail the candidate with `release_phase_unavailable`.
Old resident instances may still have a credential staged by an older binary;
snapshot invalidation alone cannot erase their memory. Verify a fresh cold boot,
a subsequent snapshot restore, sidecar delivery, and secret reload before
opening the canary gate. Explicit serving references to migration bindings must
fail with a release-only message. Ordinary manual and cron tasks must omit the
migration connection; the candidate release task must receive it.

Rotate a migration binding while a release is restoring/running. The new secret
must be published without a serving restart notification. Retirement must stay
pending until active tasks in that app and scope are terminal, then reconcile
without requiring a serving wake. Check that the schema still exists after old
login removal. A queued release must load the latest generation on dispatch.
The retirement fence conservatively includes manual/cron tasks during adoption
because older versions delivered migration credentials to those tasks too.

For starter acceptance, copy both PostgreSQL templates into a private directory,
install their Node dependencies there, and set `GREGALE_POSTGRES_STARTERS_DIR`
to that parent. With a disposable local `DATABASE_URL`, run
`go test ./pkg/managedpostgres/neon -run TestPostgresStartersUseMigrationRolesAndSerializeReleases`.
This exercises the actual migration scripts against restricted SQL roles,
concurrent release locking, runtime data access, and retained schema after
migration-login retirement. Keep the existing live version-5 Neon qualification
and rollout gates; local PostgreSQL evidence does not replace them.

## Compute resize recovery (ADR-623)

Use a fresh version 8 qualification approval before allowing new Neon intents.
The qualification changes the disposable primary's class and restores it,
checking data, existing logins and read-only permissions after both changes.
Existing pending resizes remain reconciled when provisioning is disabled.

Inspect the request using `gregale postgres resize-status DATABASE REQUEST_UUID`.
A provider timeout leaves the database `updating`; preserve the operation and
its pinned IDs. Recovery reads provider configuration before considering a
mutation. A changed dataset, default branch, unexpected configuration, missing
backend or expired worker lease prevents completion. Repair the provider/config
cause and allow reconciliation; do not delete the intent or edit generations to
force readiness. There is no customer cancellation or rollback endpoint yet.
Monitor the `updating` database reconciliation metrics and pending request's
`last_error_code`. Provider changes can interrupt connections.


## Existing database idle policy changes (ADR-624)

Apply the additive compute-policy migration and deploy the matching apid and
reconciler binaries before enabling this capability. Keep provisioning closed
until fresh qualification v7 proves both idle policy directions, actual
suspend/wake, preserved data and credentials, stable replay and restoration.
Earlier approval artifacts cannot authorize this contract. Qualification now
defaults to twenty minutes; an explicit timeout must leave room for two real
five-minute idle periods as well as provisioning and credential checks.

Check `gregale postgres capabilities --json` for `scale_to_zero_update` and
`always_on`. The former declares update support; disabling suspension also
requires the plan's always-on entitlement. The selected database's pinned
backend must support updates, even if the regional default does.

```sh
gregale postgres compute-policy orders --scale-to-zero false --request-id UUID
gregale postgres compute-policy-status orders UUID
```

Use `true` to re-enable idle suspension. Both the boolean and UUID are required.
Reuse the same UUID after a timeout; do not substitute a new request. The API
returns current persisted progress, including explicit false target values.
Class resizing and policy changes cannot run concurrently. Policy changes do
not change compute limits, storage, retention, credentials or data identity.
Neon's enabled idle period remains five minutes in this increment.

An uncertain provider result retains its pending intent and confirmed prior
specification. Reconciliation continues after admission closes. Diagnose safe
`last_error_code` values and provider configuration; preserve the journal and
generations. Existing connections may disconnect and reconnect using their
existing credentials. Rollback of the additive migration is blocked once any
policy history exists so completed receipts cannot silently disappear.

## Live queries (ADR-20261016151947)

Live queries keep a SQL result current in browsers. A customer enables them
per database; enabling issues the database's live secret, which backends
seal queries with. They are an operator preview and run only on native
databases.

### Rollout gate

Live queries are invisible to customers until launch.

- **`FAAS_LIVE_QUERIES_ACCOUNTS` on apid** lists the account UUIDs that see
  them. It is empty by default, which means nobody. Set it to our own
  account to test on production; `*` admits every account at launch. An
  invalid entry stops apid at startup.
- **Outside the list:**
  - every live route answers exactly like an unregistered API route, for
    any method and without credentials;
  - the account's realtime endpoints get no live routes, so realtimed
    answers `live.*` frames like any unknown frame;
  - livequeryd is unreachable without a route.
- **Kept off the public surface until launch:**
  - the OpenAPI spec and the generated Go, Node and Python SDKs (the live
    routes are in the spec-compliance and sdk-coverage `routeExclude`
    lists);
  - customer CLI help and the CLI reference for `gregale postgres live`,
    since `gregale postgres` is operator-only.
- **Already released** (2026-10-20, Data API reads only): `gregale data-api
  live enable|status` in customer help, and `@gregale/data/live` with
  `./live/react` and its README section. Outside the rollout, `data-api live`
  reports that live queries are not available.
- **Launch** means setting `*`, then restoring the spec entries and the
  generated SDKs.
- Removing an account from the list hides the routes and stops new live
  subscriptions within the 30-second endpoint reconciler. Sessions already
  open keep their subscriptions until they close; disable the database's
  live queries to revoke its keys at once.

### Operator setup

1. Generate an age identity for livequeryd on each database node. Give apid
   only its recipient (public key) file, and point
   `FAAS_APID_LIVE_KEY_RECIPIENT_FILE` at it. The file's mode must not grant
   group or other write. Without it, the live routes answer
   `live_queries_unavailable`.
2. Set `live_hub_url` on each backend whose databases run live queries, for
   example `"wss://live.fsn-1.internal:9480/{database_id}/live"`. apid
   substitutes the database ID, and realtimed connects there.
   - Backends without `live_hub_url` answer `live_queries_unsupported`.
   - On `neon` backends, run livequeryd with `-release-after` and
     `-awake neon` (step 4). Otherwise its replication connection keeps
     each database's compute awake.
   - The fingerprint excludes `live_hub_url`, so adding it moves no
     database.
3. Apply migration `20261017121336518_managed_postgres_live_keys` before
   deploying the apid that serves the routes.
4. Run livequeryd with these flags. The `livequeryd_service` Ansible role
   does this on the control plane (`faas_livequeryd_managed: true`; see its
   README): it passes secrets as systemd credentials, wires apid's recipient
   file, and opens the hub port to compute nodes. It issues the hub's mTLS
   server leaf from the fleet CA; `faas_realtime_live_tls_managed: true` on
   compute hosts gives realtimed its client leaf, and `pki_renew.yml` renews
   both. Which accounts see live queries is the `live_queries` platform flag
   (ADR-1700). One livequeryd serves every database the role lists in
   `faas_livequeryd_databases` (`-databases-file`), each at
   `/<database_id>/live`. With `faas_livequeryd_discover_backends`, it also
   serves every live database of those backends whose provider issued a live
   login when live queries were enabled (the Neon provider does), with no
   listing. The flags below serve a single database, for development.
   - `-database-url-file`: a file holding the database connection string
     (`-database-url` puts its password in the process list);
   - `-keys-dsn-file`: a file holding a read-only control-plane connection
     string;
   - `-keys-identity`: the age identity, mode 0400;
   - `-listen` for realtimed, with `-tls-cert`, `-tls-key` and `-client-ca`
     on any shared network.

   It reads the database's keys every `-keys-refresh` (5 seconds), so
   revocations reach open subscriptions within that. The metrics
   `livequery_keys` and `livequery_key_refreshes_total{result}` show the
   ring and its reads.

   **Scale to zero.** With `-release-after 5m`, a hub with no transaction
   to apply for five minutes checkpoints and closes every connection to
   the database (replication and pooled), so the database can park or
   suspend.
   - Subscribers keep their results. A new subscriber to a result the hub
     holds is served without the database.
   - The hub reconnects, and catches up exactly from the slot, when:
     - `-awake` reports the database running again;
     - a subscription needs a result it does not hold (an RLS-mode read
       always does, because it compiles against the catalog).
   - `-awake neon` polls the Neon API every `-awake-every` (5 seconds) with
     `-neon-api-key-file`, `-neon-project` and `-neon-endpoint`. Commits made
     while released reach subscribers within that interval.
   - A native database's wake will come from schedd, which is not wired to
     livequeryd yet, so `-release-after` refuses to start without `-awake`.
   - Metrics: `livequery_released` (1 while released),
     `livequery_releases_total`, and `livequery_resumes_total{reason}`
     (`database_active`, `subscription`, `woken`, `failed`).
   - The slot-lag metric is not read while released, because reading it
     would wake the database.

   **Neon prerequisites, not yet verified on a Neon database:**
   - logical replication enabled for the project;
   - the schema-change log's event triggers (`-install`), which need a role
     allowed to create them;
   - the slot surviving a suspend and resume. A lost slot is recovered by
     reading every result again.
5. Give realtimed `FAAS_REALTIME_LIVE_TLS_CA_FILE`. When livequeryd sets
   `-client-ca`, also give it `FAAS_REALTIME_LIVE_TLS_CERT_FILE` and
   `FAAS_REALTIME_LIVE_TLS_KEY_FILE`. Roll realtimed out before apid.
   apid registers each endpoint with the live routes of databases bound to
   its app, and an older realtimed rejects that field.

An app reaches a database's live queries only through its own realtime
endpoints, and only while it holds a ready binding to that database. Other
seals are refused with `forbidden` in realtimed.

### RLS mode

Browsers can also subscribe to a Data API read as the signed-in user. Row-level
security then decides the rows, with no backend seal.

- **Which login a query reads as.** apid puts the app's binding logins on
  each live route, and realtimed names the login on every query; clients
  cannot name one.
  - Sealed queries read as the app's read-only binding, else its
    read-write binding. An app with neither is refused (`forbidden`).
  - RLS-mode reads run as the app's Data API binding (ADR-650), in its
    `api` schema unless the read names another. An app without a Data API
    binding is refused.
  - livequeryd refuses a login that is a superuser or bypasses row-level
    security.
  - Binding changes reach realtimed with the endpoint reconciler, within
    30 seconds.
- **livequeryd**
  - Its database login is the superuser that `-install` needs, so it can
    become every binding login. A login without superuser needs `SET` on
    each.
  - It believes a named login and RLS claims only from a client
    certificate that `-client-ca` verified (realtimed's).
- **Endpoint.** The app's realtime endpoint must use `oidc_jwt`. realtimed
  verifies each token against that endpoint's issuer and JWKS before the
  database sees its claims.
- **Cost.** Each distinct set of claims is maintained separately. A change
  to a table that a policy reads re-reads the sessions it affects, and a
  policy calling a plpgsql function re-reads its sessions on every write.
  Prefer SQL functions in policies.

### Customer commands

```sh
gregale postgres live enable orders --app web    # stores GREGALE_LIVE_SECRET on web
gregale postgres live status orders
gregale postgres live rotate orders --overlap 24h --app web
gregale postgres live disable orders             # revokes every live secret
```

- **Enable and rotate** return the secret once.
  - With `--app`, the CLI stores the secret as an ordinary app secret.
  - Otherwise, it prints the secret and the customer stores it.
  - apid seals the secret to livequeryd's recipient and cannot read it
    back, so a lost secret must be rotated, not recovered.
- **Rotate** keeps the previous secret until `--overlap`: 24 hours by
  default, at most 7 days. `--overlap 0` revokes it at once. Any older
  secret is removed, so a database holds at most two.
- **Disable** removes every key, and livequeryd stops opening their seals.
- A deleting or deleted database serves no keys.
- Each change is audited as `managed_postgres.live.enabled`, `.rotated` or
  `.disabled`, with the key ID. Secrets are never logged.

## Failed creation custody and compensation

Apply `20261007112111335_managed_postgres_creation_receipts.sql` before deploying
creation-custody recovery (ADR-638). The private creation journal is separate
from verified restore proofs and retained snapshot receipts. Its requested point
is an intent fence; it is never evidence of restored contents or retention.

A successful Neon creation response with complete ownership metadata is recorded
before correctness polling. After a verification failure or worker takeover,
recovery observes that exact physical ID. Compensation independently rechecks
its source, operation owner, resource type and creation time, then confirms
physical absence after deletion. A durable cleanup checkpoint records prior
independent visibility; early absence of a creation that has never been observed
cannot finish compensation. Missing capture timestamp or expiry can still
block readiness while an independently owned failed resource is cleaned up.
Snapshot compensation does not invent a retained receipt, and database cleanup
preserves the physical accounting identity and outstanding usage holds.

Do not remove source projects to compensate failed customer resources. Resources
without an acknowledged ownership receipt retain conservative full-proof
recovery. Missing inventory entries do not establish that an uncertain creation
never happened. Keep provisioning disabled: PITR timestamp/LSN mapping, snapshot
capture and retention evidence, restored login/data isolation, and completed
settled usage qualification remain open.
