# Function VM execution lifetime — portable evidence

Date: 2026-10-16. Branch: `codex/function-vm-lifetime`, based on
`6cc957b22ba04767bad5ec467db4af826209ee15`. Scope: ADR-1347's acknowledged
VM execution ownership, idle/checkpoint admission and all-interpreter
lifecycle callbacks. This report is committed with the implementation; Git
history identifies that revision. The dependency PR #4768 remains closed and
unmerged. This work does not reopen it or resume its merge automation.

## Implemented behavior

- The Node runner requires an explicit host reservation before dispatching
  user code. Guest-init authenticates the runner's kernel peer PID, stamps
  its generation and forwards a bounded RPC over the VM-scoped vsock
  listener. The authoritative plan comes from the host lease. No application
  header, manifest or environment variable activates this wiring.
- HTTP response completion and execution completion remain separate. The
  host counts handler, stream and post-response lifetime until the actual
  worker cleanup fence releases it. Lost replies, duplicate registration or
  completion, cancellation tombstones and stale VM/runner generations do
  not manufacture successful execution or a zero count.
- A temporary boot ledger covers startup health handlers and transfers to
  the published Instance without an idle window. Ordinary Begin holds an
  incarnation-pinned flight through quiet resume before ACK. Simultaneous
  freeze and execution registration cannot both succeed.
- Stats and quiet/freeze/reaper activity observe execution occupancy without
  increasing the HTTP throughput counter. Capture rejects active work before
  effects and seals hook admission at the final pause boundary. Scheduler
  park establishes idle admission before state/capacity changes and snapshot
  reuse. A busy warm-capture refusal keeps the live VM and its reservation.
- Checkpoint and restore callbacks visit every loaded interpreter. A batch
  keeps new acquisition closed until all callback/cancellation release fences
  settle. Checkpoint HTTP acknowledgement also requires the host's completion
  reply. Detached work is forbidden inside these lifecycle callbacks.
- Expiry or runner-generation loss seals admission and retires the original
  VM through its existing teardown reservation. A timeout never decrements
  the count. Uncertain cleanup retains physical recovery ownership. Successful
  teardown keeps replacement boot fenced while the synchronous schedd failure
  notification joins the completed stop result. vmmd adds no instance-row writer.

## Portable checks

Toolchains: Go 1.26.9 linux/amd64, Node 24.19.0 and 22.23.3, Python 3.12.14.
Tests used local process/listener access and `umask 022`. The following scoped
race checks passed:

```sh
go test -race ./guest/runners/... ./pkg/rootfs -count=1 -timeout=3m
go test -race ./pkg/functionlifetime ./pkg/fcvm/activity ./guest/init \
  ./guest/runners/internal ./guest/runners/internal/workerpool -count=1 -timeout=3m
go test -race ./pkg/fcvm ./pkg/sched \
  -run '^TestFunctionExecution' -count=1 -timeout=2m
go test -race ./pkg/fcvm ./cmd/vmmd ./pkg/vmmdgrpc \
  -run '^Test(FunctionExecution|GuestVsock|Freeze|Quiet|Park|WarmSnapshot|SnapshotKeepAlive|InstanceFlight|Destroy)' \
  -count=1 -timeout=3m
go test -race ./pkg/sched ./pkg/e2etest \
  -run '^Test(FunctionExecution|DaemonBuildTagsMatchesTestBinary)' -count=1 -timeout=2m
```

The real runner tests include HTTP/1 and HTTP/2 early completion, a host reply
held before any handler side effect, dropped registration and completion
acknowledgements, neighbour isolation, aggregate-cap refusal through cleanup,
and checkpoint callbacks in four distinct Node processes. A forbidden lifecycle
`waitUntil()` must fail instead of acknowledging a detached callback.
Protocol/host tests additionally cover bounded/malformed frames, duplicated
ownership, stale incarnations, startup, runner restart, watchdog retirement,
restart quarantine, lost replies and concurrent freeze admission. The worker
batch tests keep acquisition closed through delayed cancellation cleanup.
An injected unconfirmed child exit retains activity, the live Instance,
allocator lease and pending cleanup; a subsequent confirmed retry releases
them only after physical teardown.

The new runner/background/batch tests also passed with Node 22 first on PATH:

```sh
go test -race ./guest/runners/internal ./guest/runners/internal/workerpool \
  -run '^Test(Execution|Background|LifecycleBatch)' -count=1 -timeout=2m
```

Two existing guest-init sidecar tests assumed the development host's kernel
booted from virtio-blk. This workspace boots from pmem, which shifts device
letters. Their fixtures now explicitly select their intended virtio-blk
layout; existing table-driven drive tests retain both layouts. The complete
guest-init race suite passes with `umask 022`. A run without that umask failed
existing file-mode fixtures; this was corrected in the invocation, without
weakening their assertions.

### Real TLS/SCRAM PostgreSQL

A fresh disposable PostgreSQL 16 container used the CI-pinned image digest,
SCRAM host authentication and a one-day localhost certificate. The test role
was generated for an isolated database and granted only the fixture INSERT
access. DSNs/passwords were not printed or committed. The following passed
with `FAAS_TEST_FUNCTION_POSTGRES=1`, a private `DATABASE_URL` and public
`NODE_EXTRA_CA_CERTS`:

```sh
go test -race ./guest/runners/... ./pkg/rootfs -count=1 -timeout=3m
go test -race ./pkg/fcvm -run '^TestFunctionExecution' -count=1 -timeout=2m
```

The existing actual HTTP runner SQL cancellation/replacement test passes. The
new `TestFunctionExecutionPostgres` independently locks a table, witnesses a
restricted TLS SQL session blocked after the adapter's HTTP 202 frame, refuses
host park and freeze, then releases the lock and observes task settlement,
execution completion and one persisted row. Duplicate completion leaves
occupancy at zero. Its Manager uses a fake VM boundary; this is not native
SQL/capture/restore evidence. Disposable roles/databases, container, volume,
certificate/private key and fixture credentials were removed.

### Build and policy checks

The native e2e test package compiles with
`metal,function_background_native`. Focused function ownership, scheduler
admission, receiver readiness and harness build-tag race checks pass with
those tags. These tests use fake VM boundaries; selecting `metal` does not
make them native execution evidence.

Scoped production-package golangci-lint, workflow actionlint, ADR uniqueness,
runbook SQL, text encoding, shell quoting and documentation link checks pass.
The whole native-tagged `cmd/e2e` lint reports 23 existing findings in untouched
dev-loop/watch, private TCP, build/deploy and jobs fixture files; none names
the new function background test. Changed-code native lint is checked separately.
It passes with `--build-tags metal,function_background_native --new-from-rev=HEAD`.
The docs live-server tests require local listener permission; the restricted
sandbox initially denied socket creation and the same checks pass with local
network access enabled. No assertion or branch protection was bypassed.

The `shared workers and TLS PostgreSQL` CI job now runs the new protocol,
guest-init, activity and scheduler/host admission checks, including the real
SQL gate. The path filter covers the new ownership/lifecycle source. No
GitHub CI result or PR merge is claimed by this local report.

## Native boundary and activation

`make test-function-background-native` was attempted. It fails its strict root
prerequisite on this workspace, which also lacks `/dev/kvm`. Direct invocation
of `TestFunctionBackgroundMetal` after successful tagged compilation likewise
fails with `function background qualification requires root on native x86_64 Linux`.
`make leakcheck` fails closed because `/dev/loop0` through `/dev/loop7` are
unavailable; it cannot inspect native loop resources. These are prerequisite
failures, not passing native or leak qualification.

The existing strict concurrency command was also run on this revision:

```sh
FAAS_REQUIRE_FUNCTION_CONCURRENCY_METAL=1 \
  RUN_REGEX='^TestFunctionConcurrencyMetal$' make test-metal PKGS=./cmd/e2e
```

Its helper/test builds completed; execution fails with
`function qualification requires root on native x86_64 Linux`.

The new strict native test source covers ordinary deployed Node 22 functions:
three occupied interpreters after HTTP completion, busy capture/freeze/park
refusal, exact-once file witnesses, all-worker checkpoint callbacks, frozen
thaw and real terminal capture/restore. It has not executed on a supported
native host. Its file witnesses do not qualify PostgreSQL draining.

Both Node 22/24 mains, guest-init and vmmd wire this feature only when built
with **both** `metal` and `function_background_native`. Normal production
builds remain off. No deployment, durable execution, native VM-wide SQL pool
draining, performance parity, billing/fleet telemetry qualification or Python
background support is claimed. Private environment-qualification restores
do not expose this port yet. Native lost-reply/crash/deadline/restart/uncertain
cleanup fault injection and real SQL draining across interpreters remain
required before activation. See the
[acceptance procedure](../../function-backend-concurrency-acceptance.md).
