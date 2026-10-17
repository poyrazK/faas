# Function migration destination — local implementation evidence

Date: 2026-10-17. Branch: `codex/function-postgres-lifecycle`.
Base: `d76c4ee3a974b59755f020c6b4bf7a4e2be6583f`.

This milestone implements [ADR-1352](../../../adr/1547-function-migration-destination.md):
restore a measured Node function destination, transfer ownership and retire the
source. Production activation remains disabled. This is portable evidence;
native two-host KVM/TLS SQL execution is not qualified. PR #4768 remains closed
and its automation remains paused.

## Implementation

- Source vmmd records the measured capture under its original permission before
  reporting successful preparation. Destination vmmd issues a fresh permission
  binding that capture, current application inputs and artifact approval, exact
  destination node/incarnation, wake, native payload and migration lease.
- Only the dedicated qualification capability permits the same logical instance
  to restore from its measured migration capture. Ordinary snapshot clones keep
  their identity and catalog rules. Migration has no cold-boot fallback.
- The host retains a single physical destination attempt and its receipt. Native
  restore uses one resume-hook transport attempt; duplicate adoption does not
  load the snapshot or replay recovery callbacks. The existing Node adapter
  rebuilds and probes registered PostgreSQL pools before its all-worker ACK.
- Readiness leaves execution admission closed. Schedd checks current scope,
  inputs, permission, incarnation, lease and consumed bytes, then atomically
  publishes the destination epoch, network identity and runtime configuration.
  A separate activation reads committed authority before opening execution.
  The original immutable boot/admission records remain intact.
- Rollback fences the lease before retiring a destination. Source recovery stays
  unavailable until destination retirement is confirmed. Same-incarnation
  absence is tombstoned under the adoption mutex so a late RPC cannot start a
  destination after cleanup. Unknown owners after restart remain quarantined.
- A refused source recovery permission permits authenticated, lease-pinned
  physical retirement without resume. Transient store or transport failures do
  not supply that authority. Vmmd never writes instance ownership/completion.
- Pending destinations reserve database-enforced RAM, CPU and vCPU independently
  of optional service-capacity policy. Unconfirmed cleanup retains that claim;
  scheduler restart and generic lease cleanup cannot silently discard it.
- Lost ownership ACKs resolve against the committed destination epoch. Expiry
  reconciliation can retry activation and source retirement. Custom callbacks
  still require application idempotency for side effects across an aborted move
  and later source recovery; interrupted customer invocations are not replayed.

Public APIs, protobufs, SDKs and deployment activation are unchanged. The existing
authenticated migration RPCs carry the internal phases. The workflow now includes
destination tests in ordinary and qualification builds.

## Portable validation

Toolchain: Go 1.26.9 linux/amd64; `-p 1`, `GOMAXPROCS=2`, `GOGC=50`,
`GOMEMLIMIT=5GiB`, `umask 022`. Large builds run sequentially. Inactive derived
Go cache files were removed as needed within the workspace disk limit; sources,
modules and unrelated fixtures were retained.
Scoped lint uses `GOMEMLIMIT=7GiB` and a 20-minute allowance.

`portable-results.json` records exact commands, log hashes, package conclusions
and explicit skip reasons:

| Suite | Passing tests/subtests | Skipped | Failures |
| --- | ---: | ---: | ---: |
| Migration state, race detector | 51 | 0 | 0 |
| Runtime/host/scheduler/RPC, ordinary build, race detector | 350 | 45 | 0 |
| Same runtime packages, qualification tags, race detector | 408 | 6 | 0 |
| New migration replay safety | 1 | 0 | 0 |

The state suite executes real disposable PostgreSQL 16/SCRAM transactions and
MemStore equivalents. It covers exact capture/receipt matching, committed epoch
history after source cleanup, commit/fence races, environment scope, expiry,
policy/incarnation drift, and pending capacity through rollback and retirement.
The first-claim regression holds the issuing transaction's capacity lock while no
pending row is visible and requires an ordinary admission to wait for that lock.
Host tests cover blocked execution before commit, duplicate adoption, no fallback,
failed load/probe/witness, policy drift, cleanup uncertainty, cancellation joined
to restore, late adoption after cancellation and restart ownership refusal. A
duplicate activation also resolves while the committed destination is executing
a request, without another load or resource callback.

The ordinary skips include qualification-only host paths; those destination paths
run in the qualification build. Its six remaining skips are the separate opted-in
TLS SQL execution fixture and five persistent-artifact retirement tests requiring
an ext4/XFS/Btrfs disk. None is represented as passing native or SQL migration
acceptance. Injected process/load witnesses do not exercise real KVM or vsock.

The migration applies from a fresh disposable schema and passes
`TestNewMigrationsAreReplaySafe` for version `20261017134639239`. `schema.sql` is
dumped from that catalog. All 88 SQLC files match clean temporary regeneration
with sqlc v1.31.1.
An initial database run exposed the restore-method enum (`WAKE_RESTORE = 1`),
and another exposed stale-input re-admission during rollback fencing. Both were
corrected before the passing final runs. Scope checks now agree in both stores.
Final review also closed a first-claim visibility race by serializing ordinary
residency with destination issuance before inspecting pending claims.
Interrupted disk-full or compilation iterations are not passing evidence.

Documentation links, workflow syntax, ADR-number uniqueness, sealed-environment
scope, operator SQL, shell quoting and text encoding checks pass. The docs-link
HTTP checker also passes its four local tests.
Scoped golangci-lint passes with zero issues in ordinary and qualification builds.
A timed-out preliminary run is excluded from passing evidence. Changed core-path
tests satisfy the ADR citation rule; `cited-core-tests.json` records the local
staged-content check. Commands and log hashes are retained in `validation.json`.

## Native qualification boundary

The strict native target cannot run here: UID 1000, no `/dev/kvm`, no designated
acceptance-host marker and no configured kernel. The required leak inspection
also cannot inspect `/dev/loop0` through `/dev/loop7`. These are missing native
prerequisites, not successful native or leak acceptance.

`make test-function-background-native` exits 2 at the root prerequisite. The
standard `make test-metal` target builds vmmd and its jail helper, then its
selected snapshot test fails because `FAAS_TEST_KERNEL` is not configured
(exit 2). `make leakcheck` exits 2 because the eight loop devices cannot be
inspected. `native-checks.json` retains their actual outputs and false native
acceptance flags.

The complete end-to-end package compiles with
`go test -p 1 -race -tags metal,function_background_native -c ./cmd/e2e`.
`native-build.json` records its 240,781,977-byte executable, source-only SQL
suite symbols and SHA-256
`e7bec9af9d714c95b17ab2edbf455a8eeac2ae1232a8d4fcab3db1c8a2b34110`.
The executable was never run and was removed after hashing. Compilation does
not establish a native destination move.

Before activation, implement and execute the two-host matrix in
[the acceptance runbook](../../function-backend-concurrency-acceptance.md).
For Node 22 and 24 it must witness eight source sessions closing before capture,
eight fresh probed destination sessions before readiness, closed traffic until
ownership commit, preserved committed rows, distinct physical/network owners and
confirmed source retirement. Cover actual lost ACKs, failure/cancellation/expiry
races, capacity pressure, host death/restart, private-environment authority and
leaks. The existing source-only native SQL suite does not establish destination
acceptance; no automated two-host destination harness is claimed by this change.

No push, pull request, merge or deployment is performed in this milestone.

## Cleanup and source identity

The task-owned PostgreSQL container and volume were removed after their exact
names and `faas.task=function-migration-destination` labels were verified. No
task-labeled containers or volumes remain. The private credential directory and
hashed native executable were removed. `cleanup.json` records those results;
the onboarding fixture and unrelated resources were retained.

`source.sha256` identifies the 60 implementation, schema, generated-code,
workflow and documentation files outside this evidence directory. The test logs
are represented by hashes and structured conclusions, without database
credentials or unfiltered fixture output.
