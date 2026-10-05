# Managed PostgreSQL scheduler accounting: internal KVM diagnostics

Date: 2026-10-02.

ADR-396's four real-guest cases passed: liveness, workload OOM, operator restart
and cold-boot watchdog. Each injects an unconfirmed scheduler stop while an actual
Firecracker guest remains running. RAM, CPU, per-app and per-deployment concurrency
remain reserved; a new Engine rebuilds that capacity from the retained instance
row. A confirmed retry then destroys the guest and releases admission.

These are nested-node diagnostics, not the supported native x86_64 KVM release
gate. The test uses MemStore and an in-process Engine→Manager adapter with a
simulated failed destroy RPC. Database-backed RPC/fleet crash recovery and durable
drain receipts remain separate work. Customer cutover activation remains disabled.

## Source and host

- Validation commit: `6ffb385e27414fcd2e72b0d0d5f3e3d0673062f4`, tree
  `373a14ea014c3a0dbe5034f6710bab0361bb78ae`.
- Parent: `ce9e435260221b4a4d2bc4851467ce983f66d4d5` (ADR-395).
- Selected source archive SHA-256:
  `9f6a21aebc058287c92f15f1ddb626a694414c7fc7f0e71fcdf8952537615948`.
- Guest supplement SHA-256:
  `ea8c237be8424f526d452b24edb1bf8bce4b2743d14f9185117478f3112a4037`.
- Both archives came from that commit and their checksums passed before extraction.
  `validation.json` records the selected paths. The original full archive was
  interrupted during transfer and was not used for the final run. Later changes
  add documentation/evidence only; all Go sources and go.mod/go.sum stay identical.
- Project `gregale-prod`, zone `us-east1-b`, instance
  `gregale-internal-test-1`, fleet-excluded internal test node.
- Linux `7.0.0-1011-gcp`, x86_64, `/dev/kvm`, Go `1.25.13`, Firecracker/jailer
  `1.7.0`, `n2-highmem-2` (two vCPUs), `enableNestedVirtualization=true`.
- Reference SSD latency acceptance disabled. No production daemon, database
  binding or customer activation was deployed.

The run required root, the acceptance-host marker, the shared acceptance lock
and inactive Gregale VM/build/image/gateway services. Its registered stage used
an owned six-GiB tmpfs for sources, fixtures and private build/module caches;
903 MiB of existing modules were copied there. The transient unit supplied a
private four-GiB build TMPDIR and one-GiB jail filesystem, with MemoryMax=10G.
`dispatch-command.txt` and `runner.sh` retain the invocation. Exact-source
static guest-init and immutable two-drive busybox HTTP fixtures were built.

## Results and scope

`make test-metal` ran `./pkg/fcvm ./pkg/sched` with `-race -count=1`, the explicit
`RUN_REGEX` in `runner.sh`, and `-timeout=20m -v`. This selects ADR-396's scheduler
regressions, existing ForceRestart/liveness checks, real boot and 100-cycle
Park/Wake, startup failure/OOM/job exit, and ADR-394/395 admission/teardown cases.
It is not the unfiltered native acceptance suite.

| Selected checks | Passed | Skipped | Failed |
| --- | ---: | ---: | ---: |
| Top-level tests | 19 | 0 | 0 |
| Including subtests | 82 | 0 | 0 |

The second row includes the first; they must not be added together. Unselected
tests are outside these counts.

The new `TestMetalSchedulerRetainsAccountingUntilConfirmedTeardown` passed all
four cases in 3.86 s. The 100-cycle Park/Wake check passed in 86.92 s with its SSD
latency gate disabled. fcvm completed in 168.150 s; scheduler checks in 1.272 s.
The unit finished inactive, Result=success, ExecMainStatus=0, and metal.exit=0.
All three in-run leak checks passed. An extra host-wide check did not start:
another acceptance job held the shared lock beyond its 60-second wait. That job
was not interrupted. This does not replace native release acceptance.

Earlier setup attempts failed before tests: a premature dispatch ran before the
source transfer completed; a later fixture build hit the full shared module-cache
disk and the selected archive's missing guest/executor. The successful run added
the pinned guest supplement and private module cache. `initial-dispatch.log` and
`failed-build.log` preserve those failures. Intermittent Google API/OAuth DNS/TLS
failures also delayed transfer/dispatch.

Logs were downloaded before cleanup. The owned stage tmpfs was unmounted and
canary cleanup recorded state=completed and removed its directory. The final
unit is inactive; no owned test guest or stage remains. Failed setup-unit state
was reset. Other jobs' data, caches and services were preserved.

## Portable checks and limitations

The complete scheduler, scheduler RPC and state suites passed (33.238 s, 9.268 s
and 2.323 s). Scheduler/RPC lint and changed-code fcvm lint reported zero issues.
A broader fcvm lint invocation reported the existing contextcheck warning at
manager.go:5102 in SnapshotKeepAlive recovery; that code is unchanged by ADR-396.
Its recovery context originates from the registered flight. The warning is
retained in fcvm-baseline-lint.log, not silently counted as a clean full lint run.

An optional local race build was stopped during local disk pressure; the
successful Linux race run above exercises the changed regressions. Text encoding,
shell quoting, sealed-env scope, ADR uniqueness and changed core-test citations
passed. No additional dependencies or schema migrations were introduced.

Committed text normalizes carriage returns and trailing whitespace only.
SHA256SUMS hashes committed evidence; RAW_SHA256SUMS records original bytes,
retained under this task's local checks directory. A credential-marker scan of
the downloaded logs found no matches.
