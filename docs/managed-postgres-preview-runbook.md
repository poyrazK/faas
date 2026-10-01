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
after completing the supplier review and publishing the processor notice. Keep
the signed DPA and assessment in the restricted evidence store. Copy the
published [`subprocessors.json`](compliance/subprocessors.json) to the
operator-owned deployment path after adding the supplier with its verified
notice publication and effective dates. Create the internal decision from the
[`approval template`](compliance/vendor-assessments/managed-postgres-supplier-approval.example.json),
replace every placeholder, bind it to the configured backend ID and
fingerprint, record the restricted reviewer identity, and keep it in the
restricted store. A conditional acceptance also requires an explicit
satisfied flag and a restricted evidence reference for condition completion.
The approval records only opaque evidence references.

Verify all rollout evidence before applying the qualification `approval_env`
values:

```sh
FAAS_ENVIRONMENT=staging \
FAAS_MANAGED_POSTGRES_CONFIG=/etc/faas/managed-postgres.json \
FAAS_MANAGED_POSTGRES_SUPPLIER_APPROVAL_PATH=/var/lib/faas/managed-postgres-supplier-approval.json \
FAAS_MANAGED_POSTGRES_SUBPROCESSOR_REGISTER_PATH=/etc/faas/subprocessors.json \
FAAS_MANAGED_POSTGRES_QUALIFY_APPROVAL_PATH=/var/lib/faas/managed-postgres-qualification.json \
go run ./cmd/managed-postgres-qualify --verify
```

Verification is read-only: it checks the report digest, expiry, lifecycle
checks, provider-neutral spec, exact configured backend fingerprint, and
canary allowlist, then checks the accepted supplier decision, restricted DPA
and risk references, current database-category register entry, signed-DPA
claim, and matured 30-day notice without contacting Neon. The JSON reports
`qualification_readiness`, `supplier_readiness`, and combined `readiness`. A
non-zero exit or any readiness reason blocks rollout. Treat either approval as
expired according to its recorded expiry; repeat the review or qualification
instead of extending an artifact by hand.

When `FAAS_MANAGED_POSTGRES_QUALIFY_APPROVAL_PATH` is configured on `apid`,
the provisioning gate loads that artifact at startup and validates it against
the configured backend and current canary list. The artifact is authoritative:
missing, malformed, stale, tampered, or mismatched approval keeps provisioning
disabled even if the legacy `FAAS_MANAGED_POSTGRES_QUALIFIED*` variables look
valid. Those variables are a fallback only when no qualification approval path
is set. The supplier decision and register snapshot are always required,
regardless of the qualification path. Missing, malformed, expired, unsigned,
or mismatched supplier evidence keeps provisioning disabled. Restart `apid`
after replacing any of the three artifacts so the new documents are loaded.

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
