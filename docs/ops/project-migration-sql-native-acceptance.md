# Managed PostgreSQL expand/contract qualification

The `project-migration-sql-only` lane qualifies the application-SQL and credential
boundaries of ADR-20261019120017. Adding or compiling it does not qualify production;
contract admission remains disabled by default.

`TestProjectMigrationSQLSuccessMetal` and
`TestProjectMigrationSQLInterruptedMetal` must independently PASS. They reuse the
native progressive deployment harness, real source builds, APID, schedd, vmmd,
gateway cohorts, stabilization and elapsed graph/code retention. Only provider
management is simulated. The customer PostgreSQL database, roles, TLS sessions,
sealed production-scoped credentials, and guest SQL are real.

The old app reads `legacy_value` and advances a shared counter. Expansion adds
and backfills `current_value` without replacing the row or resetting its counter.
Old and new ingress cohorts must both continue to read the original marker and
advance that counter. The new app reads only the new column, so it can continue
after cleanup drops the legacy column. Independent customer-database reads verify
preserved values/counters, actual schema columns, and that old SQL fails with
PostgreSQL's undefined-column error after cleanup.

Serving guests must have no migration secret. Every SQL serving probe tries to
create a schema object using its runtime credential and requires permission denial.
A real ordinary manual task must successfully use runtime SQL; another manual
task that chooses the cleanup command must fail for lack of its migration grant.
Expansion and the server-recorded cleanup task receive the migration-only grant.
Changing command text cannot elevate an ordinary manual task.

The interrupted cleanup drops the column in an open transaction, then holds it
with `pg_sleep`. The test observes that real backend transaction before killing
schedd. Its lost exit receipt must become `uncertain`, with no automatic retry.
The SQL session must quiesce and the transaction must roll back, preserving the
expanded schema and committed data. Only then does an explicit operator SQL
transaction finish cleanup. The deployment API continues to refuse another
cleanup task for that attempt; operator SQL recovery is not an API force/retry.
The held SQL connection has a five-minute proxy deadline, so a short proxy timeout
cannot masquerade as the injected scheduler interruption.

## Run

Use the `e2e-native.yml` dispatch lane `project-migration-sql-only`, or stage an
exact source archive and use the guarded runner:

```bash
FAAS_E2E_PHASE=project-migration-sql-only \
FAAS_E2E_SOURCE_SHA="$qualified_revision" \
FAAS_E2E_GO=/path/to/pinned/go/bin/go \
bash scripts/ci/run-native-e2e.sh
```

Follow [native CI](e2e-native-ci.md) and the existing
[managed PostgreSQL prerequisites](managed-postgres-native-acceptance.md).
The dedicated native Linux amd64 host needs root, KVM, real guest-init/kernel/base
assets, and an isolated PostgreSQL cluster with TLS and SCRAM enabled. A cluster
without TLS is rejected; no guest SQL encryption/authority checks are bypassed.

The runner exclusively owns `198.18.0.254/32` during this lane. It refuses an
already-owned address and removes its address during cleanup, including failure.
The frontend declares only that destination and TCP 5432 through ordinary app
policy. The proxy preserves PostgreSQL TLS and exposes only the disposable cluster.
No production egress policy or managed service is changed.

Both tests use `metal,contractacceptance,managed_postgres_contract` tags. Their
150-minute test budget fits a 155-minute systemd envelope. Missing, skipped or
failed scenarios reject the lane. Preserve the exact source SHA, both PASS lines,
`managed contract native:` observations, generic contract receipts, and final
zero-leak evidence. Contract/canary flags are enabled only inside the private
harness. An operator must review native evidence before enabling admission.

## Available validation and limits

Portable PostgreSQL tests execute the same transactional SQL routines against
private customer databases. They verify old/new compatibility, counter/data
preservation, interruption after staged DDL, rollback, and explicit recovery.
They use an administrative connection and therefore do not qualify credential
delivery or guest isolation. Those are native-only checks.

```bash
go test ./pkg/e2etest/migrationprobe
go test -tags=metal,contractacceptance,managed_postgres_contract ./cmd/e2e \
  -run '^Test(ProjectWorkerReleaseFixtureMeta|JobTimeoutCoversEveryPhaseCap)$'
bash scripts/ci/native-e2e-phases_test.sh
bash scripts/ci/run-native-e2e_test.sh
bash scripts/ci/native-e2e-combined-verdict_test.sh
```

Full/qualify CI now runs source and runtime phase groups as two serialized
240-minute jobs. Each independently restores services and stops the acceptance
node. A combined verdict requires both jobs to succeed, verifies each artifact's
source SHA/group, and checks the original full required chain across both logs.
Targeted qualification lanes run once. Test timeouts were not reduced to hide the
former six-hour full-matrix budget problem.

This is one controlled schema evolution pattern, not proof that arbitrary
application migrations are backward compatible or transactionally reversible.
The workspace SQL/preflight checks can pass without native VM execution. Native
SQL credential delivery, interruption behavior and zero-leak qualification remain
pending on the designated host.
