# Deployed PostgreSQL starter qualification expansion — 2026-10-12

The acceptance suite now includes the embedded `rest-api-postgres` customer
starter alongside the existing Go pool probe. Thirteen mandatory phases cover
normal source deployment and migration, restored SQL sessions, failed-hook cold
fallback, task credential isolation, public API rotation while running and parked,
scheduler recovery, data preservation and independent cleanup.

## Executed local evidence

- Real PostgreSQL 16 with TLS, SCRAM and separate restricted writer, reader and
  migration roles; disposable catalogs and customer databases.
- `TestManagedPostgresFixturePublicRotation` passed with race detection: actual
  authenticated routes, production loader, qualification gate, age/HMAC secret
  delivery and binding reconciler; held/fresh old logins rejected, replacement
  login writes the shipped SERIAL schema, both distinct notes retained, completion
  persists across API restart. No VM or scheduler is present in this portable test.
- Local API compilation retained all production files and these new regression
  test files. A private Go overlay omitted 734 unrelated API test files because
  the full local race build exhausted disk while writing its package archive.
  The full tagged package is invoked separately by CI; the narrowed local pass
  is not a complete API-suite result.
- Source/certificate validation, authenticated fixture management, restricted
  SERIAL privileges, remote revocation and one-shot handshake recovery passed
  with race detection. The source test compares embedded files/manifest without
  rewriting handlers or release commands.
- The existing explicitly enabled materialized starter SQL recovery suite passed
  against the same TLS fixture with Node 26.5.0. CI uses Node 22.
- The production driver rejection test passed with race detection. The
  complete tagged deployment test package compiled locally in `go test -c` mode
  (Darwin arm64), without executing TestMain or guests. An initial `-run ^$`
  attempt reached TestMain's daemon builds and timed out; it is not a passing
  execution result. CI uses compile-only mode on Linux. Pinned golangci-lint
  2.14.0 reported zero issues in the fixture and starter packages.
- Seven strict-verdict contract tests and runner shellcheck passed. Older
  probe-only evidence is rejected because it omits the starter phases.

## Remaining acceptance gate

The expanded guest scenarios have not executed. The internal GCE node
`gregale-internal-test-1` reports virtual-machine host class `google`, so it can
only provide nested diagnostics. Its read-only preflight also failed leakcheck
with 13 existing jail chroots and leftover mounts, and its root disk had roughly
440 MB free. Another kernel build was active on the scratch disk. No VM run was
started and those unrelated resources were left untouched.

Supported native x86_64 Linux KVM execution on a clean designated host remains
required. Compilation, SQL-only tests and CI do not prove guest restore,
confirmed teardown or scheduler retirement. This fixture also does not qualify
Neon transaction pooling, Internet/DNS egress, provider PITR, billing or rollout.

No Neon key/resources were created, no plan changed, and production provisioning
was not enabled. Sanitized test outcomes are recorded in `checks.txt`; private SQL
URLs, passwords, key material and raw catalog logs stay outside the repository.
