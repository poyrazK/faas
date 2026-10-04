# Managed PostgreSQL confirmed teardown: internal KVM diagnostics

Date: 2026-10-02 Europe/Istanbul (2026-10-01 UTC).

ADR-395's real-guest regression passed on the internal node. It leaves a
Firecracker guest running after an injected stop failure, checks that its
network identity and lease remain owned, rejects resume, and confirms that a
second stop times out while an owned teardown retry is unfinished. The retry
then stops the guest and releases its resources.

These results are diagnostic evidence. The GCE node uses nested virtualization;
supported native x86_64 Linux KVM acceptance and customer cutover activation
remain pending. Local daemon ownership does not constitute durable fleet drain
proof or recover guests surviving a vmmd crash.

## Source and host

- Validation snapshot: `c3c5d7caa4dae3de5a56388eeea2c4afcd123568`, tree
  `9860bd1f57bf484c49f302bc0b47d04d1a6cb785`.
- Parent: `27377c4ade3c401bfedd51b3122974f78e3ea2e1` (ADR-394).
- Source archive SHA-256:
  `3fa38a37ab94dc41bc0c4aab226e007480e78fb52de327559e56f8fd5108d31c`.
- Project `gregale-prod`, zone `us-east1-b`, instance
  `gregale-internal-test-1`, a fleet-excluded internal test node.
- Linux `7.0.0-1011-gcp`, x86_64, `/dev/kvm`, Go `1.25.13`,
  Firecracker/jailer `1.7.0`, machine `n2-highmem-2` (two vCPUs).
- GCE `enableNestedVirtualization=true`; the reference SSD latency gate was
  disabled. No production daemon or customer binding was deployed.

The archive checksum was verified before extraction. The run used root, the
acceptance-host marker, the shared `/var/lock/faas-builder-acceptance.lock`, and
a private four-GiB tmpfs at the short path `/run/mpg-c3c5d7ca`. Other acceptance
jobs were not interrupted. The runner refused to proceed with active Gregale
VM/build/image/gateway services. Exact-source guest-init and immutable two-drive
busybox HTTP fixtures were built for this run.

## Scope

The broad metal-tagged package ran through `make test-metal` with `-race`,
`-timeout=20m -v -skip=^TestMetalBoot50Concurrent$`. This is a diagnostic selection,
not the unfiltered native release gate. The one excluded concurrent-capacity
case already fails on this two-vCPU nested host on the pre-ADR-394 baseline;
[that baseline evidence](../20261001-managed-postgres-vmmd-admission/README.md)
remains available. Specialized fixture-dependent tests report their own skips.
A second batch runs all six mount/network namespace tests under private
`unshare --mount --net` namespaces with `FAAS_TEST_NETWORK_BATCH=1`.

## Results

| Phase | Passed | Skipped | Failed |
| --- | ---: | ---: | ---: |
| Metal-tagged fcvm package, including portable tests | 600 | 20 | 0 |
| Private namespace batch | 6 | 0 | 0 |
| Total | 606 | 20 | 0 |

`TestMetalBoot50Concurrent` was excluded explicitly and is additional to the
20 reported skips. Six namespace tests skipped in the first pass and all passed
in their isolated batch. Fourteen other fixture/platform-dependent tests remain
unexercised, including the specialized paused-Park deadline fixture. The complete
skip list and each reason are in `package.log`.

The new `TestMetalTeardownRetainsOwnershipUntilConfirmed` passed in 1.06 s.
ADR-394 admission and late-resume regressions passed in 2.20 s and 1.07 s.
The 100-cycle Park/Wake test passed in 95.73 s with the reference-SSD latency
release gate disabled. Startup-failure, OOM and clean-exit job cases passed.
The transient unit finished inactive with `Result=success`, `ExecMainStatus=0`;
both test-phase exit files contained zero. All four pre/mid/post leak checks
passed, reporting no leaked namespaces, TAPs, jails, cgroups, processes or mounts.

`run.log`, `package.log`, `namespace.log`, `runner.sh` and `host.json` retain the
run output, exact diagnostic invocation and host qualification. Committed text
normalizes carriage returns and trailing whitespace only. `SHA256SUMS` hashes
those committed files; `RAW_SHA256SUMS` records the original downloaded bytes,
retained under the local task's checks directory. The source archive checksum
above refers to the exact archive checked on the node. The final change retains
the same Go sources; later edits add documentation and evidence only.

## Portable checks

The complete fcvm package, vmmd RPC package and cmd/vmmd tests passed. Scheduler
stop, deleted-app/account cleanup, park, recycle and task/execution cancellation
regressions passed. Targeted fcvm race checks, changed-package lint (including
tests), text encoding, shell quoting, sealed-env scope, ADR uniqueness and
changed core-test citation checks passed.

Local verification required a short TMPDIR for Unix sockets; the final full
fcvm run passed after correcting that invocation. A local Linux cross-compile
was stopped during disk pressure; the Linux metal binary was built on the node.
Only this task's disposable Go cache was cleared. macOS race linking emitted
its existing LC_DYSYMTAB warning and completed successfully.
