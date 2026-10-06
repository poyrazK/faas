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
`FAAS_MANAGED_POSTGRES_QUALIFY_LIFECYCLE=true` and save its JSON output in an
operator-owned path. The output includes a versioned approval envelope and the
exact `approval_env` values for the staging gate. Verify the saved artifact
before applying those values:

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
passes; rerun qualification instead of extending it by hand. Version 6
artifacts require runtime DML and RLS enforcement, denied DDL/administration,
stable migration ownership, and preserved data after migration login retirement.
When read-only access is advertised, approval also requires actual existing and
future table/sequence reads, denied mutations/DDL/administration and RLS bypass,
stable recovered passwords, rotation preserving data, and revoked old sessions
and fresh logins. A capability declaration alone is insufficient.
They also require a restore timestamp inside the disposable source's lifetime,
target readiness, earlier committed data, rejection of source credentials on
the target, and completed deletion. Versions 1–5 cannot authorize this release.

When `FAAS_MANAGED_POSTGRES_QUALIFY_APPROVAL_PATH` is configured on `apid`,
the provisioning gate loads that artifact at startup and validates it against
the configured backend and current canary list. The artifact is authoritative:
missing, malformed, stale, tampered, or mismatched approval keeps provisioning
disabled even if the legacy `FAAS_MANAGED_POSTGRES_QUALIFIED*` variables look
valid. Those variables are a fallback only when no approval path is set and
`FAAS_MANAGED_POSTGRES_QUALIFIED_VERSION=6` matches the current contract.
Unversioned environment approvals remain blocked.
Restart `apid` after replacing the artifact so the new document is loaded.

## Staging canary rollout

Keep the global qualification gates enabled only in the isolated staging
environment. To start with one or a small set of accounts, set
`FAAS_MANAGED_POSTGRES_CANARY_ACCOUNTS` to their exact account IDs separated by
commas. Verify a database create and binding smoke for each listed account,
then expand the list deliberately. Leave the variable empty only when every
qualified staging account is an intentional canary. The gate is fail-closed on
malformed entries and never blocks database deletion or credential revocation.

## Admission failures

- `managed_postgres_usage_stale`: stop new reservations, verify the provider
  usage API and the usage collector logs, then wait for a complete window. Do
  not bypass the gate by raising a plan limit.
- `managed_postgres_quota_exceeded`: inspect the account's plan allowance and
  the operator usage policy. A plan storage ceiling is an entitlement; the
  monthly compute, history, egress, and spend ceilings are COGS safeguards.
  Upgrade or reduce the workload rather than disabling metering.
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

## Account deletion

The account status transition is blocked while any managed database is not a
provider-confirmed `deleted` tombstone. Ask the customer to delete the
database and wait for reconciliation. The final deletion transaction removes
only deleted managed-Postgres metadata and usage rows; it never erases an
active provider resource as a shortcut.

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
fresh version 6 live Neon qualification passes. Local PostgreSQL tests establish
SQL behavior; they do not establish Neon password recovery or branch isolation.

Version 6 replaces prior approvals, including version 4; keep provisioning
closed until the new disposable live run and lifecycle smoke pass. Inspect
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

Use a fresh version 6 qualification approval before allowing new Neon intents.
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
until fresh qualification v6 proves both idle policy directions, actual
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
