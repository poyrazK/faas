# Measured function migration source permissions — local evidence

Date: 2026-10-17. Base: `2a8c6551526399db76c5bde443ce75503bb2e5b9`.

This milestone adds explicit capture and recovery permissions to the source-only
Node qualification path. It does not activate production migration or destination
adoption. This milestone does not reopen PR #4768 or change its paused automation.

## Implementation

- The host pins the original physical VM before requesting capture permission.
  One transaction commits the source, capture token, immutable file handles and
  permission before checkpoint effects. The existing native input, security,
  producer approval and artifact fences remain mandatory.
- A permission binds purpose, nonce, measured serving receipt, source wake,
  migration lease identity and clocks, source start, Firecracker version and
  memory/VM-state/private-drive keys. Serving history identifies the original
  owner; it does not supply fresh permission.
- Capture permission expires by the existing admission TTL, artifact approval
  deadline and migration lease. schedd must fence rollback before recovery
  permission can issue, including after an expired lease. Exact recovery retries
  preserve a valid permission so they cannot invalidate an in-flight final ACK.
- The host checks durable permission before and after effects, holds its egress
  policy gate and enforces the permission deadline even on destruction-joined
  recovery that outlives an RPC. The existing one-attempt callback and physical
  cleanup fences remain in force. Bounded physical teardown can finish after
  permission expiry and cannot authorize another resume. Duplicate replies do
  not replay callbacks.
- Initial MIGRATING and final RUNNING publication recheck current inputs and
  permission. Retirement can complete STOPPED without inventing resume authority.
  Stale authority before effects retains the source and capacity for reconciliation.
- Permissions stay internal to the authenticated source RPC. Existing warm/park
  grants, public APIs, protobufs, SDKs, entitlements and deployment activation
  are not expanded.

## Validation

Toolchain: Go 1.26.9 linux/amd64, `-p 1`, `GOMAXPROCS=2`, `GOGC=50`,
`GOMEMLIMIT=5GiB`, shared build cache and `umask 022`. Large Go checks run
sequentially. Inactive derived cache artifacts were removed as needed to stay
within the workspace disk limit; source and module caches were retained.

The ordinary-build race run covers state, fcvm, sched, vmmdgrpc and
runtimeadmission: **308 passing tests/subtests, zero failures**. Its 31 explicit
skips are recorded with reasons in `portable-results.json`; the database tests
ran against disposable PostgreSQL 16 with SCRAM and did not skip. The
`metal,function_background_native` build has **135 passing tests/subtests,
zero failures**, with only the separately opted-in application SQL fixture
skipped. These tags exercise injected host boundaries and do not prove native
execution.

The durable permission suites cover atomic registration, input/incarnation drift,
capture expiry, fresh recovery after an expired lease, strict persisted decoding,
permission renewal and the SQL permission-pair constraint. Host tests cover
wrong scope/keys/parent, cancellation, held ACK, deadline, failed probe,
post-ACK drift, lost registration ACK and rejection after capture. A rejected
or uncertain attempt never replays application callbacks. The existing
source ownership, snapshot and execution suites also pass.

The migration passes `TestNewMigrationsAreReplaySafe` with
`FAAS_REPLAY_CHECK_VERSIONS=20261017122213034`: one pass, no skips. An initial
database run exposed a CHECK-expression parenthesis error; it was corrected
before the successful fresh-migration, replay and regression runs above.

All **87** SQLC files match clean temporary generation with sqlc v1.31.1.
Public APIs and protobufs are unchanged. Documentation links, workflow syntax,
ADR-number uniqueness, sealed-environment scope, operator SQL, shell quoting and
text encoding checks pass. Changed core test files have valid ADR citations.
Scoped golangci-lint passes with **zero issues** in both ordinary and
`metal,function_background_native` builds. The latter also checks the new native
SQL harness. `validation.json` retains the commands and log hashes.

## Native qualification boundary

The new strict SQL matrix requires measured protocol-v2 boot admission for
Node 22 and 24. It covers cancellation, real scheduler expiry, capture cancellation,
failed SQL probes and a held recovery ACK timeout. Its assertions require eight
sessions to close before capture and fresh pools before the final recovery ACK,
preserve the original source wake and committed rows, and check duplicate
cancellation without a pool-generation change. The held ACK case is controlled timeout
injection, not evidence of lost real-vsock packets.

Native KVM/vsock execution, failure/restart, private-environment and leak
qualification remain required. Compilation and injected host boundaries do not
establish native acceptance or Vercel performance parity. Production stays off;
there is no deployment, push, PR or merge in this milestone.

`make test-function-background-native` fails its root prerequisite. The standard
`make test-metal` target built the vmmd daemon, jail helper and selected snapshot
test, then failed because `FAAS_TEST_KERNEL` is not configured. `make leakcheck`
fails because `/dev/loop0` through `/dev/loop7` cannot be inspected. This workspace
also has no `/dev/kvm` or native acceptance-host marker. `native-checks.json`
preserves those outcomes; none is passing native or leak evidence.

The complete end-to-end package compiles with
`go test -p 1 -race -tags metal,function_background_native -c ./cmd/e2e`.
Its symbol table includes `TestFunctionMigrationAdmissionPostgresMetal`.
Executable SHA-256:
`a5f447bb11476b54bb756386656ee2fe8f217c5a389c32024786e9ebe3e327cb`.
`native-build.json` retains the exact command, size and symbols. The executable
was removed after hashing and was never executed. Implementation source hashes
are retained in `source.sha256`.

## Cleanup

The task-owned PostgreSQL container, its volume and private credential directory
are removed after validation. `cleanup.json` records the exact ownership label
and confirms no task container, volume, credentials or native test executable
remain. No production database or deployment is used.
