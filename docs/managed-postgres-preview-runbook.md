# Managed PostgreSQL preview runbook

This runbook covers the dark/isolated preview. Customer provisioning remains
disabled until the staging qualification evidence is reviewed and the exact
backend fingerprint is approved.

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
