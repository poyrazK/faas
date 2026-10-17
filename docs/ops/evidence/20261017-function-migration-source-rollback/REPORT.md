# Function migration source rollback — local evidence

Date: 2026-10-17. Base: `af8dd0344a475aea1b60e471a4adeb6235a2475b`.

This is local implementation/qualification evidence. Production activation is
off. Migration destination adoption and measured migration capture remain
refused. PR #4768 remains closed and unmerged; its automation remains paused.

## Implementation

- A qualified Node source binds the original wake, source node, migration token
  and physical incarnation. Parent capture handles and the durable source receipt
  commit together before checkpoint effects; publication keeps the guest paused.
- schedd atomically replaces the original ownership token with a rollback token
  while retaining MIGRATING. Initial marking requires a prepared, unexpired source
  and cannot reuse the versioned token after completion or cleanup.
- vmmd checks the durable fence before and after one resource recovery attempt.
  Ordinary invocation, forward, resume, freeze and capture paths cannot open the
  prepared source. All registered interpreters' pools use ADR-1349 recovery.
- Duplicate cancellation/expiry reuses the completed result, with no callback or
  SQL replay. A lost ACK, failed probe or unfinished checkpoint retires the
  original VM; unconfirmed cleanup retains allocator and scheduler ownership.
- The internal RPC carries both confirmed retirement and a source protocol
  version. An older empty reply does not authorize RUNNING. schedd completes
  RUNNING or STOPPED only after confirmation, then permits capture/lease cleanup.
- Admission repair preserves source concurrency and RAM even across a legacy
  rollback that cleared the instance token. Tracker accessors return isolated
  snapshots; the generic lease releaser cannot discard source authority.
- Ambiguous registration retains a known no-effects host result and its lease.
  Missing durable source authority never falls back to generic resume/retirement.

## Portable verification

Toolchain: Go 1.26.9 linux/amd64, `-p 1`, `GOMAXPROCS=2`, `GOGC=50`,
`GOMEMLIMIT=5GiB`, shared build cache and `umask 022`. Large Go checks run
sequentially.

The final ordinary-build race run covers state, fcvm, vmmdgrpc, sched and
runtimeadmission: **270 passing tests, zero failures**. Its 18 explicit skips
include the qualification-only Node cases, the separately opted-in TLS/SCRAM
application SQL fixture, and the existing memstore peer-rollback placeholder.
The matching `metal,function_background_native` race run has **300 passing
tests, zero failures**, with only the opted-in application SQL fixture and the
existing memstore placeholder skipped. These tags exercise injected host/guest
boundaries; they do not establish native execution. Commands, package results,
explicit skips and raw-log hashes are retained in `portable-results.json`.
Counts include parent tests and subtests.

After the final test-harness adjustment, the focused source-protocol gRPC race
test passes again in both ordinary and qualified builds (no skips). It uses
`wire.DialContext` with a context-aware in-memory connection, preserving the
standard client deadline interceptor, and checks all six protocol-version and
retirement combinations. Results are retained in `client-results.json`.

The PostgreSQL source suites execute against disposable PostgreSQL 16 with SCRAM
on loopback. They exercise actual ownership transactions, concurrent commit vs
rollback, pre-Phase-2 fencing, expired/late marking, cleanup identity, and duplicate
completion at the node memory ceiling. They do not establish native snapshots or
application SQL pool recovery across a migration. The final ordinary race run
executes these SQL tests without a PostgreSQL skip.

The new migration's actual SQL also passes `TestNewMigrationsAreReplaySafe`
with `FAAS_REPLAY_CHECK_VERSIONS=20261017094227428`: one pass, no skips.

A prior run hit the local disk limit (SQLSTATE 53100) and was discarded. Inactive
derived Go cache artifacts were removed; source and module caches were retained.
The rerun above passed. Its tracker assertion now checks equal data and isolation
from both inserted and returned objects, replacing the obsolete pointer-identity
expectation after getters became snapshots.

## Generation and source policy

- All **20** generated protobuf files match clean temporary regeneration with
  CI's pinned Go v1.36.11 and gRPC v1.6.2 plugins; no version header drift.
- All **86** generated SQLC files match clean temporary regeneration with
  sqlc v1.31.1.
- ADR-number uniqueness, sealed-environment scope, operator SQL, shell quoting,
  text encoding, documentation links and whitespace checks pass.
- The backend CI job now includes migration filters and real PostgreSQL source
  tests, and its trigger includes state/schema/migration/protocol changes. Actionlint
  passes for the changed workflow.
- Qualified changed-code golangci-lint reports **zero issues** for state, fcvm,
  sched and vmmdgrpc against the base above. The first attempt timed out and
  reported three context-analysis findings: two calls intentionally use the
  destruction-joined recovery context, and a checkpoint test simulates the
  synchronous receiver ledger, which has no context argument. Those precise
  call sites now document their existing context ownership; no lint configuration
  or runtime cancellation contract was changed. The cached rerun passes.
- Ordinary changed-code golangci-lint also reports **zero issues** for the same
  packages and base. These scoped checks do
  not assert that unrelated baseline findings or full-repository lint are clean.

## Native boundary

`make test-function-background-native` exits 2: this workspace is UID 1000 and
requires root on the designated isolated native KVM acceptance host.
`make leakcheck` exits 2 because `/dev/loop0` through `/dev/loop7` are absent.
`/dev/kvm` is absent and `FAAS_TEST_KERNEL` is not configured. These are unavailable
native prerequisites, not passing native/leak evidence.

The standard metal target was also attempted:

```sh
RUN_REGEX='^TestMetalNativeSnapshotCaptureMeasuresWarmAndParkAndPreservesRetry$' \
  make test-metal PKGS=./pkg/fcvm
```

Its daemon, helper and test builds completed. The selected fixture failed
immediately because `FAAS_TEST_KERNEL` does not name a readable kernel (exit 2).
`metal.log` and `native-checks.json` preserve these prerequisite results. This
does not qualify a snapshot or a real recovery.

The tagged end-to-end test package compiles successfully:

```sh
go test -p 1 -tags metal,function_background_native -c \
  -o /tmp/function-migration-native.test ./cmd/e2e
```

Executable SHA-256:
`5914016884968fad671117b16081515e9603194d1a602d47a455d1f61dfeaebf`.
The executable was removed after hashing. This establishes compilation only.
Implementation source hashes are retained in `source.sha256`.

Actual source capture/recovery, real-vsock loss/duplication,
watchdog/uncertain cleanup, vmmd restart and private-environment qualification
remain outstanding. A restart without the original in-memory owner grants no
permission to reconstruct an empty execution ledger or replay recovery callbacks.
Fresh measured capture/destination/cancel-resume authority and failure drills are
still required before expanding activation. No deployment or rollout is performed.

## Cleanup

The disposable PostgreSQL container, its single database volume, private
credential directory and metadata file are removed. The task-label container
listing is empty and the exact volume no longer exists. `cleanup.json` records
the confirmation without secrets. No production credentials are used.
