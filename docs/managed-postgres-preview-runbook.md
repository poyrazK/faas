# Managed PostgreSQL preview runbook

This runbook covers the dark/isolated preview. Customer provisioning remains
disabled until the staging qualification evidence is reviewed and the exact
backend fingerprint is approved.

## Qualification approval

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
passes; rerun qualification instead of extending it by hand.

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
qualification report says restore usage is isolated. With the Neon adapter,
shared-project restore branches remain unavailable while usage guardrails are
enabled because provider consumption is project-scoped.

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
