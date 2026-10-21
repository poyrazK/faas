# ADR-20261021094500: Project submission retention

## Status

Implemented. Amends ADR-20261019120002 (durable project submission receipts).

## Context

Every project apply with a `Deployment-Attempt-ID` writes a
`project_submissions` row whose payload carries the selected workloads and any
safe release state. Nothing removed these rows, so the table grew with every
deploy. App-level receipts do not have this problem: they live for the
deployment's lifetime (ADR-20261019120049).

A receipt cannot simply expire by age. Safe release reconciliation reads it
while the release builds, verifies, rolls back or monitors stabilization. Other
release state names the attempt and reads its receipt later: progressive
canaries, migration contracts and their recoveries (including the replacement
attempt), paired configuration intents, worker release candidates and service
addition reservations, whose retries require the owning receipt to be finished.

## Decision

apid prunes receipts hourly from the safe project release worker, in batches of
500 and at most 20 batches per pass. A receipt is pruned only when all hold:

- it was last updated more than `api.ProjectSubmissionRetention` (30 days) ago,
  which exceeds the seven-day client session retention, so a saved session can
  always recover its outcome;
- its safe release, if any, is not `building`, `verifying` or `rolling_back`,
  and stabilization is not monitoring;
- no attempt-keyed release state names it: `project_release_canaries`,
  `project_migration_contracts`, `project_migration_recoveries` (attempt or
  replacement attempt), `project_release_config_intents`,
  `deployment_project_release_workers` or `project_release_additions`.

Postgres selects candidates oldest first with `FOR UPDATE SKIP LOCKED`, so
concurrent apid replicas never block each other or a live writer. MemStore
applies the same rules.

## Consequences

The table stays bounded by recent and still-referenced attempts. Receipts held
by release state live as long as that state; their own retention is unchanged.
A pruned receipt reads as not found, which clients already treat as
"acceptance uncertain" rather than rejection.

## Validation

`TestProjectSubmissionRetention{Memory,Postgres}` covers age, advancing
release statuses and batch bounds; `TestProjectSubmissionRetentionKeepsReferencedReceiptsPostgres`
covers a referenced receipt.
