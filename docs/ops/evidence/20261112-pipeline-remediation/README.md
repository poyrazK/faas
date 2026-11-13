# Pipeline remediation qualification — 2026-11-12 UTC

Implementation and local qualification for [PIPE-001–016](../../../pipeline-findings.md),
following [ADR-20261112220000](../../../adr/20261112220000-pipeline-findings-remediation.md).
The working tree includes the earlier retention/read-availability/discovery batch.

The historical investigation probes assert defective behavior. The normal Go and
Node regressions now assert intended behavior; do not rerun the historical probes
as acceptance tests.

## Scope and environment

Go 1.26.10, PostgreSQL 16 with TLS and native logical replication, the repository's
digest-pinned PostgREST binary, and pinned Node runtime dependencies. All databases,
accounts, API tokens and source failures are disposable local fixtures. No live
provider, production account, deployment, webhook delivery or KVM snapshot-resume
qualification was performed. Durable restart enqueue, snapshot invalidation and
idempotency are tested at the catalog/outbox boundary; signed HTTP behavior is
verified by restarting the real local API through that callback.

## Executed checks

The complete canonical native acceptance script passed (exit 0) after the final
corrections, covering all seven package groups with the race detector. The worker
package completed in 613.320 seconds. The earlier ownership-conflict failure is
preserved in [this historical excerpt](full-suite-before-pause-order-fix.log);
it was corrected and passed both focused coverage and the final full run.

| Check | Result | Evidence |
| --- | --- | --- |
| Complete `scripts/test-data-pipeline.sh`: worker, pipeline/source APIs, dashboard, managed Postgres, Neon credentials and CLI | Pass, race enabled; exit 0 | [full acceptance](native-full-acceptance.log) |
| Final worker: ownership conflicts, pause, retired generations, credential rotation, verification/cancellation, all three new native recoveries, hook contracts and migration | Pass, race enabled (39.371 s) | [native worker](native-worker.log) |
| Credential installation/publication serialization and bounded credential integration | Pass, race enabled | [concurrency](credential-concurrency.log) |
| Managed-Postgres and Neon acceptance stages, plus new native recoveries | Pass, race enabled | [acceptance](native-credential-acceptance.log) |
| Verification, queued cancellation, durable refresh and migration regressions | Pass (86.236 s) | [verification](verification.log) |
| Legacy queue backfill across multiple batches/replay, retained proof and empty readers | Pass | [migration](migration.log) |
| API-server dashboard pipeline wiring | Pass | [API wiring](api-wiring.log) |
| CLI pipeline/check/connection-budget regressions and Markdown reference | Pass, race enabled (7.263 s) | [CLI](cli.log) |
| Final signed HTTP late-hook scenario and child-process teardown | Pass, race enabled | [API lifecycle](native-api-lifecycle.log) |
| Data API runtime | 198 pass, 1 skip, 0 failures | [runtime](runtime.log) |
| Node SDK TypeScript build; Python empty-reader/retention model round trip | Pass | [Node](node-sdk-build.log), [Python](python-sdk-smoke.log) |
| Canonical schema generation, migration lint, sqlc, spec compliance, SDK generation and docs links | Pass | [contracts](schema-and-contracts.log) |

Migration lint used the reviewed baseline commit because this checkout has no
ancestor release tag. The schema was dumped from a fresh disposable database with
all migrations applied; that database was removed afterwards. Embedded OpenAPI
and generated CLI reference parity and `git diff --check` also passed.

- Native acceptance: `GOMEMLIMIT=6GiB bash scripts/test-data-pipeline.sh` (race detector).
- API wiring: `make test-apid RUN='^TestDashboardPipelines'`.
- Verification and migration regressions: `go test -count=1 -run
  '^(TestVerification|TestSchemaBackfill|TestReadGateRefresh|TestQueuedVerification|TestPipelineFindingsMigration)'
  ./pkg/datapipeline`.
- Runtime: `npm test` in `cmd/gregale/templates/data-api`.
- Code generation: canonical schema dump from all migrations, `make sqlc-check`,
  Node and Python SDK generation, Node SDK TypeScript build, and Python model
  round-trip of empty readers and retention evidence.
- Contracts/docs: `TestSpecCompliance`, migration expand/contract lint, embedded
  OpenAPI parity, generated CLI reference parity, documentation links and
  `git diff --check`.

## Regression coverage

Every finding has its implementation and named tests in the backlog. Coverage
includes final-reader revocation during grace followed by disconnect (real RLS,
retained rows); queued cancellation before source work and after pause; legacy
queue upgrade across multiple committed batches; pause acknowledgement and
monitoring loss; setup/check arbitration; credential issue/rotation/replay;
late hook installation with authorized 503 and unauthorized empty results;
primary-key/INCLUDE ordering and cursor fencing across source DDL; initial-copy
failure, durable stall observation and recovery; CLI preflight, naming, UUIDs,
stale-failure recovery and destructive resync confirmation.

A timestamp-format error in the new stall fixture was corrected and its native
regression passed. A subsequent review added query ordering to physical proof
fingerprints to fence DDL between metadata hydration and storage observation;
the verification regressions passed. Earlier runs interrupted or superseded by
these changes are not final qualification evidence.

Final worker selection (after the ownership correction):

```sh
go test -race -count=1 -timeout=12m -run '^(TestNative.*(RetiredGeneration|Pause|CredentialRotation|Verification|FinalReader|InitialCopy|ReadHooks).*|TestReadGate.*|TestPipelineFindingsMigration.*)$' ./pkg/datapipeline
```

The same disposable TLS/logical-replication setup as
`scripts/test-data-pipeline.sh` supplied this run's environment. Credential grants
and installation share a transaction lock; the deterministic concurrency test
waits for a real PostgreSQL advisory-lock waiter before committing installation.
The initial-stall fixture removes shared-cluster WAL evidence only for its alert
projection assertion, because retention pressure intentionally has higher
priority. The real native stall reason and subsequent recovery are still checked.
The API fixture now closes the latest restarted API before its test context is
cancelled and reaps PostgREST before exiting.

The final full run used the final source without further implementation changes.
Its disposable PostgreSQL container and API fixtures were cleaned up on exit.
