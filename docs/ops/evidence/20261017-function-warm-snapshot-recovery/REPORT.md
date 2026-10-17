# Node function warm-snapshot recovery — evidence

Date: 2026-10-17. Branch: `codex/function-postgres-lifecycle`, based on
`170c5af02f40a8f1a74e2d3b918c1bd5c13cd556`. Scope:
[ADR-1349](../../../adr/1574-function-warm-snapshot-resource-recovery.md).
This report is committed with the implementation; Git history identifies the revision.
PR #4768 remains closed and unmerged; its merge automation remains paused.
No deployment or production activation is included.

## Implemented behavior

- Qualified Node 22/24 warm captures drain registered resources and keep the
  guest paused through snapshot publication. A single all-worker recovery pass
  rebuilds and probes pools before admission reopens.
- A cancelled capture RPC still runs bounded recovery under the existing
  destruction-joined flight. Failed captures return no snapshot evidence.
- Lost recovery ACK, failed resume/probe, deadline or unfinished checkpoint
  cleanup seals admission before physical retirement. Unconfirmed teardown
  retains allocator and pending-cleanup ownership until a confirmed retry.
- Measured warm grants explicitly retain checkpoint authority. Failure unwinds
  the outer authority flight before joining teardown; it cannot wait on its own
  caller. Ordinary builds retain their existing warm path.
- Both RPC error mappers distinguish post-admission failure from a healthy busy
  refusal. Measured pre-capture races preserve active ownership. A failed source
  Destroy retains scheduler RAM/concurrency and the running row until retry
  confirms retirement.
- schedd re-establishes idle admission after warm recovery before terminal park
  or reuse destruction. New execution ownership wins against that park without
  losing concurrency or RAM. Migration capture remains refused.
- CI runs qualified fake-host contract checks as well as ordinary production
  build checks. The strict native target builds both Node runners and requires
  independent SQL/warm/recovery/restore/thaw/death phases for each runtime.

## Portable validation

Toolchains: Go 1.26.9 linux/amd64, Node 24.19.0 and Node 22.23.3. Go commands use
the shared cache, `GOMEMLIMIT=3GiB`/`5GiB`, `GOGC=25`/`50`, `GOMAXPROCS=2`,
`-p 1`, local-listener access and `umask 022`. Large Go builds run sequentially.
Inactive derived cache entries were evicted to retain disk headroom; source and
the module cache were preserved.

Both ordinary and `metal,function_background_native` fake-host race suites pass:

```sh
go test -p 1 -race ./pkg/fcvm ./pkg/sched ./pkg/vmmdgrpc ./pkg/runtimeadmission \
  -run '^Test(Function(Resource|Execution)|SnapshotGrant|SnapshotAcknowledgment|ManagedSnapshot|MeasuredWarm|CaptureAdmitted|StandardSnapshot|BeforeCheckpoint|CaptureWarm|FrozenRefusals)' \
  -count=1 -timeout=3m
go test -p 1 -race -tags metal,function_background_native \
  ./pkg/fcvm ./pkg/sched ./pkg/vmmdgrpc ./pkg/runtimeadmission \
  -run '^Test(Function(Resource|Execution)|SnapshotGrant|SnapshotAcknowledgment|ManagedSnapshot|MeasuredWarm|CaptureAdmitted|StandardSnapshot|BeforeCheckpoint|CaptureWarm|FrozenRefusals)' \
  -count=1 -timeout=3m
```

The qualified fcvm suite includes the real five-second recovery deadline,
held recovery ACK, failed/cancelled capture, missing capability, lost ACK,
unfinished checkpoint ownership, failed physical cleanup and successful retry,
Destroy cancellation/join and measured authority/failure teardown. The scheduler
case introduces execution after warm recovery but before delayed gateway SQL
touches, and requires the second idle barrier to preserve the original VM and
capacity. A real host framing test over a local UDS deliberately loses the ACK
and observes exactly one callback payload. This is not real-vsock evidence.
RPC mapping regressions retain internal failure classification despite nested
busy/unavailable/cutover causes and keep driver diagnostics out of responses.
Scheduler tests retain RAM on unconfirmed Destroy and release it on confirmed
retry. Final focused fcvm warm tests also pass in both build modes after the
checkpoint simulator was narrowed to the pure ledger operation used by the
receiver's hook/finish passthrough. Real ordinary admission and UDS framing
remain exercised independently.

A newly created disposable PostgreSQL 16 service uses SCRAM, hostname/IP-verified
TLS, a temporary public CA, isolated databases and restricted roles. With
`FAAS_TEST_FUNCTION_POSTGRES=1`, the full Node 24 runner/rootfs race suite and
Node 22 focused resource race suite pass:

```sh
go test -p 1 -race ./guest/runners/... ./pkg/rootfs -count=1 -timeout=3m
go test -p 1 -race ./guest/runners/internal \
  -run '^TestFunctionResources' -count=1 -timeout=3m
```

The full Node 24 internal suite took 17.827 seconds. After making the test-only
HTTP control peer close its connection and consume its response body, fresh
TLS/SCRAM SQL runs of the focused resource suite passed on Node 24 (3.356
seconds) and Node 22 (3.099 seconds). The peer therefore does not reuse an idle
HTTP connection copied into a snapshot. Four loaded interpreters and eight
actual PostgreSQL sessions
perform two continuing-process drain/rebuild cycles. Each cycle independently
observes zero sessions before checkpoint ACK, eight new sessions before held
recovery ACK, and the same three committed rows. A real worker death must not
replay its interrupted write. Separate generated-adapter cases hold all eight
restore probes, including a failed neighbour, and prohibit early completion.
These tests inject the host/VM boundary and do not establish native snapshots.

The disposable SQL container, volume and credential/certificate directory were
removed after both runs; label-filtered container and volume listings were empty.

ADR uniqueness, text encoding, shell quoting, sealed-env scope, operator SQL,
retained security rules (32), workflow actionlint and whitespace checks pass.
The complete runtime-admission race suite and docs-link check also pass.

Scoped ordinary-build golangci-lint reports zero issues for fcvm, sched,
vmmdgrpc, runtimeadmission, runner internals and the SQL fixture. The final
fixture/runner lint rerun also reports zero issues. Qualified changed-code lint,
including native e2e source, reports zero issues against base `170c5af02`.
This is not a claim that unrelated native baseline findings or full repository
lint are clean.

The final native e2e test package compiled with both qualification tags:

```sh
go test -p 1 -tags metal,function_background_native -c \
  -o /tmp/function-warm-native.test ./cmd/e2e
```

Executable SHA-256:
`5b9426ad472d34e5b64cd9ffc995111fc00f8f585eca8b17c6e81a994296e08a`.
The executable was removed after recording its hash. This establishes compilation
only. Source hashes are retained in `source.sha256`.

## Native boundary

The strict native target was attempted and failed its root prerequisite:

```text
make test-function-background-native
requires root on the isolated native KVM acceptance host
exit status 2
```

`make leakcheck` also failed because this executor has none of `/dev/loop0`
through `/dev/loop7`. This is a missing acceptance environment, not a successful
leak check. `/dev/kvm` is absent. No native phase is silently counted as passed.

The standard metal command was run as well:

```sh
RUN_REGEX='^TestMetalNativeSnapshotCaptureMeasuresWarmAndParkAndPreservesRetry$' \
  make test-metal PKGS=./pkg/fcvm
```

Its daemon, helper and test builds completed. Execution failed explicitly because
the native fixture has no readable `FAAS_TEST_KERNEL` (exit status 2). It does not
qualify physical warm capture or restore. The strict SQL target additionally
requires positive captured memory/VM-state lengths rather than accepting an
empty successful response.

Native KVM execution, actual warm snapshot restore and fault injection over real
vsock, watchdog/uncertain cleanup, vmmd restart/recovery and private environment
qualification remain outstanding. Compiled native test source does not qualify
these cases. Production activation stays off; no deployment, performance parity,
durable execution, billing change or exactly-once SQL guarantee is claimed.
