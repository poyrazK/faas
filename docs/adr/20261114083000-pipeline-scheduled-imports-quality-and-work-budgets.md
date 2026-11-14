# ADR-20261114083000: Scheduled pipeline snapshots, quality checks and work budgets

- Status: implementation for approved staging qualification
- Date: 2026-11-14
- Amends: ADR-1591, ADR-20261113205500

## Decision

Scheduled snapshots are a separate creation-time mode. They use a bounded,
read-only repeatable-read source transaction, require SELECT and supported table
metadata, and never create a publication, slot or subscription. Both endpoints
still require fresh qualified connection capacity and the existing approved
account, provider, verified TLS and network gates. Source and target reservations
cover two control connections each, with no native replication resources.

The first release imports at most 100,000 rows and 64 MiB within 45 seconds.
It copies selected columns through bounded, typed text batches into private
temporary tables and verifies every staged row against the same source snapshot.
Only then does a short atomic target transaction replace all selected public
tables. Public table identities, grants and RLS remain stable. A committed private
run receipt recovers lost catalog acknowledgements without recapturing or falsely
advancing capture time. Failed or interrupted refreshes retain the previous
snapshot. Schema changes require a newly reviewed pipeline in this first release.

Owners select intervals between one hour and one day and optional daily UTC work
windows. Missed intervals coalesce; failures wait for the next interval instead
of flooding the source. Explicit run requests respect the work window. Capture
age and failed attempts remain visible separately from worker observation age.
Snapshot mode does not retain source sessions between runs. Provider compute
sleep and billing remain provider-dependent.

Scheduled quality checks run on the mirror using structured required, unique,
numeric range and complete-primary-key reference rules. No caller SQL is accepted.
They return counts/status only, never failing row values. Scans and query time are
bounded; an incomplete scan never becomes a passing assertion. Rules run in one
read-only repeatable-read target transaction against owned, readable tables.
Failures notify through the existing debounced incident/recovery outbox without
rewriting data or closing row access. Replication verification and business data
quality remain separate observations.

Work windows, query deadlines and per-tick batch budgets apply to optional
verification/backfill/quality/snapshot work. Reader revocation, pause, deletion,
monitoring and native apply retain priority. A native COPY bandwidth cap and
larger concurrent index builds require separate qualification.

Catalog intent and observations use SQLC and optimistic revision/lease checks.
Private customer SQL and DDL stay in the worker, consistent with the established
endpoint exception. Expand migrations install `pipeline-scheduled-v1` fences for
pipelines, snapshot checks and quality-aware monitors; binary rollback retains
those fences. This does not enable production or qualify a live Neon source.
