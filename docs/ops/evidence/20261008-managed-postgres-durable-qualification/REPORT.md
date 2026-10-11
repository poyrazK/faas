# Managed PostgreSQL durable qualification — 2026-10-08

Local SQL and authenticated HTTP acceptance passed for ADR-731. This evidence
tests version-8 qualification with simulated provider management; it is not a
live Neon qualification artifact or authorization to enable provisioning.

## Environment and boundaries

- Go 1.25.13, macOS arm64, PostgreSQL 16.15 with native TLS and SCRAM fixture
  role authentication. Catalog connections use the local test administrator.
- Isolated migrated catalog clones and separate disposable customer databases.
- Actual `PostgresStore`, `state.PgStore`, production credential delivery,
  X25519 age envelopes and HMAC verification.
- Provider management is simulated. Provider instances, registries, services
  and catalog pools are reconstructed between lost-acknowledgement stages.
- Faults disconnect the catalog immediately after the effect, preventing normal
  error persistence. Recovery takes over the unfinished expired lease using a
  fresh pool. Test clocks advance across real lease/retry fences without sleeps.
- Main baseline: `2442c913cffd3790c690403541e56721dfda5936`; implementation is
  the accompanying PR. No production rollout, provider API key, Neon project,
  plan change or billing mutation occurred in this run.

## Results

| Acceptance | Result |
|---|---|
| Full managed PostgreSQL package suite against local SQL | PASS |
| Shared credential-delivery package | PASS |
| Qualification command, including SQL fixture preflight | PASS |
| Durable lifecycle contract | 34/34 assertions PASS |
| Lost provision acknowledgement and fresh-service replay | Same logical/physical database, one provider creation |
| Lost credential acknowledgement | Same binding and recovered generation |
| Lost encrypted-secret publication acknowledgement | Committed secret recovered without replacement binding |
| Lost rotation acknowledgement | Generation 2 recovered; existing SQL table, marker and counter preserved |
| Cleanup with provisioning closed | Provider deleted, all three credential roles revoked, catalog tombstones and secret absence verified after restart |
| Missing fault proof, preexisting foreign fixture, data loss, failed cleanup | Rejected; no valid durable approval |
| Credential substitution and store-read failure | Rejected; read failure does not prove deletion |
| Catalog session-setting spoof, foreign app, occupied secret target, unsafe key files | Rejected before provider mutation |
| Authenticated API restart and persisted idempotency replay | PASS |
| Existing credential URL, partial rotation deletion and unpublished sealer regressions | PASS |
| sqlc regeneration, package lint, repository policy gates | PASS |

The HTTP acceptance builds every platform-selected apid production file with
`managed_postgres_credentials_test.go` and
`managed_postgres_durable_api_test.go`, then runs all seven top-level tests in
those files. Full local apid test compilation exceeded available memory/disk;
the focused build passed and CI remains responsible for the entire package.
Earlier resource-related compiler and checkpoint failures were resolved by
reclaiming this task's stale private fixture and using a focused build. The
affected SQL tests were rerun successfully.

## Reproduction

Use an isolated PostgreSQL 16 cluster with TLS, SCRAM host authentication,
CREATEDB and role administration. Supply its `DATABASE_URL` privately:

```sh
FAAS_PGTEST_TEMPLATE_DATABASE=1 go test -p 1 ./pkg/managedpostgres ./pkg/managedpostgres/credentialdelivery ./cmd/managed-postgres-qualify -count=1
FAAS_PGTEST_TEMPLATE_DATABASE=1 go test -p 1 ./cmd/apid -run 'TestManagedPostgresDurableAPI|TestManagedPostgresCredential|TestManagedPostgresConnectionURL' -count=1
make sqlc-check adr-number-uniqueness-check runbook-sql-check text-encoding-check shell-quoting-check
```

CI configures native TLS in its disposable PostgreSQL service before running
the durable SQL workload tests. The new CLI SQL tests run in the PostgreSQL
shard rather than silently skipping in a database-free job. The TLS script
passes shellcheck and workflow structure validation; this Mac has no running
Docker daemon, so container execution is checked by CI.

## Remaining acceptance

Fresh live Neon version-8 qualification is required for the exact configured
placement and advertised capabilities, including Data API when enabled.
Version-7 live evidence remains historical and cannot authorize v8 provisioning.
A disposable deployed-app canary on a supported native KVM host must separately
prove guest secret injection, egress, migration execution, reconnect after
rotation, snapshot invalidation and old-credential retirement. Snapshot capture,
retention and native copy remain unqualified until complete upstream timestamp
and expiry metadata can be independently verified.
