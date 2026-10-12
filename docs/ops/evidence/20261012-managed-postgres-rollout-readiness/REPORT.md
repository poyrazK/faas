# Managed PostgreSQL combined rollout readiness — 2026-10-12

This change adds an offline verification contract. This record is portable
validation of that contract, not a successful provider or native guest report.

## Scope

- Provider v8 and durable lifecycle evidence are combined with native guest v1.
- Provider executable provenance and the independently approved guest bundle
  must identify the same clean Git revision and PostgreSQL major.
- Guest evidence covers all thirteen phases, both published runtime bases,
  every harness daemon/helper, guest init, kernel, builder base, Firecracker,
  jailer and the test executable. Before/after artifact hashes and successful
  SQL, daemon, proxy-address and host-leak cleanup precede publication.
- Staging enforcement is opt-in and fail-closed for partial policy. Offline
  verification does not contact a provider, SQL or VM daemon.

See [ADR-992](../../../adr/992-managed-postgres-combined-rollout-readiness.md)
and the [operator procedure](../../managed-postgres-native-acceptance.md).

## Local validation

Go 1.26.9 on macOS arm64, with bounded parallelism and the shared Go cache:

- Race regressions passed in `pkg/managedpostgres/guestqualification`,
  `pkg/managedpostgres`, `cmd/managed-postgres-qualify` and `pkg/e2etest`.
  Cases cover missing evidence, release/provider/base changes, dirty or
  unstamped binaries, expiry after gate startup, canary mismatch, nested
  diagnostics, malformed documents and partial-policy fallback.
- A Python-produced report passes Go verification, contains no raw transcript
  output, is private, and refuses to overwrite an existing evidence file.
- All twelve Python stream-verdict tests passed; failed/skipped phases,
  missing execution/completion, duplicate identities and old phase sets fail.
- Pinned golangci-lint 2.14.0 passed for the four affected portable packages.
  ShellCheck and `git diff --check` passed.
- `go test -race -p 1 -tags metal,managed_postgres_native -c ./cmd/e2e`
  compiled successfully with bounded API-server compiler flags. The compiled
  native test executable was not run on this Mac.
- ADR uniqueness, Go security-rule retention and operator SQL-document gates
  passed. The API-server size baseline was corrected to the exact existing
  main count (155,936); this change adds no API-server production lines.
- A clean regular clone produced real qualifier, test and Linux amd64 static
  helper executables with matching `vcs.revision` and `vcs.modified=false`.
  This caught missing automatic stamping in Git worktrees. The runner now
  requires stamping, and the operator procedure requires a regular clone and
  explicitly stamped qualifier/guest init. The tagged native compile and
  affected artifact-helper lint/race checks passed again after that fix.

## Limits and outstanding qualification

Supported execution remains pending on a clean, designated native Linux amd64
KVM host. The previously checked internal GCE host had unrelated leaked
resources and is nested; no acceptance run was attempted on it for this change.
Compilation and synthetic report fixtures cannot supply rollout readiness.

No live Neon key, provider resource, customer deployment, production setting or
billing change was made. Production provisioning remains disabled. Qualification
bundle hashes identify test-tagged executables, not production executable bytes;
operator approval must check the intended source revision and bundle. These
documents are not signed attestation from an untrusted host.

The optional full-repository text-encoding gate still reports two findings
already present on main: a fixed-event log in the MCP template and an SSE byte
buffer reset. Those unrelated files were not changed here. The PostgreSQL
contract's portable checks do not depend on those paths.
