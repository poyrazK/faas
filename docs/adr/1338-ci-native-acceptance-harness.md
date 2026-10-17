# ADR-1338: Opt-in CI native acceptance harness

- Status: harness implemented; native and real GCS execution pending
- Date: 2026-10-15
- Extends: ADR-1573, ADR-1574, ADR-1336 and ADR-1337

Service-path extension: [ADR-1339](1339-internal-ci-execution-service.md) now wires
HTTP execution admission and the bounded apid worker into this harness. The
Go-only description below records the original implementation. Native acceptance
remains pending.

## Decision

Add a separate, manually invoked native CI acceptance suite at
`tests/ci/acceptance`. It uses the existing subprocess e2e harness to run real
apid, schedd, vmmd, imaged, meterd and gateways on a dedicated Linux x86_64 KVM
host. `make native-ci-acceptance` requires explicit test opt-in, a root-owned
host marker, a clean candidate checkout, pinned toolchain, drained host, private
GCS bucket and a guest-reachable HTTPS listener with a trusted certificate.
The shared native host lock serializes the gate with existing native suites.
The runner refuses active fleet daemons and Firecracker workloads; it does not
stop or reconfigure a deployed fleet.

All production CI flags remain off, with empty bucket/origin/allowlist defaults.
Only the isolated fixture subprocesses receive enabled CI flags. No workflow,
daemon dispatcher, HTTP execution admission, image publication, IAM change or
bucket provisioning is added. The existing blocked receipt workflow is unchanged.

## Test boundary and provenance

The native suite performs fresh Go-only Jobs admission and explicitly advances
the CI engine. Source is a fixed, generated archive and a marker-owned synthetic
GitHub binding. This qualifies extraction and transport across a real VM, while
the signed GitHub ingress/provider boundary retains its separate qualification.
No repository step runs on the host. Only platform binaries and test tooling
are compiled there.

An operator supplies the immutable runner image. imaged must materialize that
exact digest before admission. Every guest verifies the candidate runner binary
SHA-256 and extracted fixture contents before running its scenario. The runner
build uses the recipe's static, trimmed, stripped flags; the guest-init binary is
also built from the candidate checkout. Both hashes and the source SHA enter
the evidence journal. Actual task receipts must retain the same image digest and
materialized key through restart and dependent-job admission. A tag, stale image
or substituted runner fails qualification.

The HTTPS fixture terminator preserves the configured host and forwards only
CI routes to the isolated loopback apid, using its existing trusted local TLS
termination contract. TLS verification is never disabled. A real private GCS
bucket supplies source/log/artifact I/O under ADR-1337 policies. A 300 KiB guest
artifact exceeds the driver's resumable chunk size, so provider acceptance
exercises the real SDK upload path rather than only small multipart objects.
Physical generation/tombstone fencing is checked again during cleanup.

## Required scenarios and evidence

The suite must complete every scenario; skipping or filtering out tests cannot
produce the required journal receipts:

1. Two dependent successful jobs, ordered starts, verified authenticated artifact
   downloads and exact residency-based metering at the existing RAM-plus-8 rate.
2. An actual user step failure with its exit code and result preserved.
3. Deadline expiry with the CI timeout reason and confirmed native stop.
4. Running cancellation with no command retry.
5. apid, schedd and controller restart retaining the original attempt/image,
   plus a deliberately lost upload acknowledgement and verified final output.
6. A distinct tenant's parked app wakes and serves through the real gateway
   while five Pro CI guests consume CPU. This is a bounded contention check;
   it does not certify full-host memory exhaustion or the reference-SSD wake SLO.

Every scenario retains real native instance IDs, one task attempt, immutable
image evidence, expected terminal outcome and stopped-instance proof. Native
identities cannot repeat across scenarios. Provider retirement, native leakcheck,
schema removal and local native-file removal are required for a passing verdict.
The runner also preserves the Go test exit code: complete-looking JSON cannot
turn a failed test process into a passing run.

## Ownership and interruption recovery

Write a private, fsynced recovery journal before allocating the random fixture
schema. Its marker binds the run, database identity, source/binary hashes, bucket,
image and origin. The database fingerprint excludes passwords, permitting
credential rotation. Cleanup refuses a modified bucket or a foreign schema.
Intent mutations use production stores; only fixture namespace DDL, read-only
evidence queries and fixture expiry adjustment use acceptance-specific SQL.
No fixture writes an instance or task completion receipt.

The journal records each admitted execution before the first engine Tick.
Cleanup cancels intent and lets the actual scheduler/VM owners confirm stop.
Active execution/controller/storage leases and missing stop evidence retain the
schema and charged reservations. Fixture-only expiry adjustment allows bounded
retirement without waiting seven days; the production retirement predicates and
TTL constants are unchanged. Retired provider keys remain empty permanent
tombstones. A late create must fail, and payload readback must be unavailable.

The test harness retains native resource journals and images under the private
evidence directory rather than automatically deleting them on a failed drain.
A normal cleanup stops the owned daemons, checks native leaks, and only then
removes the owned schema and native files. The runner's EXIT/TERM/INT/HUP path
retries intent/storage cleanup while owners are alive, terminates its process
group, and runs leakcheck, finalization and verdict checks. It never runs a
host-global reaper to hide a leak. Every cleanup step has a separate deadline.

SIGKILL and host failure cannot run traps. Retain the journal and native files,
reconcile native ownership using the existing resource journal, then retry the
cleanup/finalize helper under the shared host lock. An unresolved native or
provider failure stays failed and retains recovery authority. Recovery actions
do not turn an interrupted scenario into successful execution evidence.

## Validation limits

Portable race tests exercise opt-in/configuration refusal, evidence completeness,
private journal handling, credential-free database identity, ownership-bound
schema access and refusal to drop unconfirmed recovery authority. The complete
native suite and metal helper compile without executing a VM. Existing harness
tests cover unchanged default temporary-file/DNS behavior.

This workspace has no `/dev/kvm` and no configured cloud identities or secrets.
Its earlier leakcheck could not inspect sysfs loop entries without `/dev/loop*`.
No real native, GCS, metering, contention or image-lifetime acceptance is claimed.
Run the guarded gate on the designated host and retain its actual receipts
before wiring internal execution admission or an execution dispatcher.
