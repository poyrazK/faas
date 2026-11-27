# Environment diagnostics and durable completion — 2026-11-15

**All nine functional scenarios passed with real Firecracker VMs. Eight signed
completion deliveries survived an actual scheduler restart. Initial and final
physical leakchecks passed; preview writers remain off by default.**

The [sanitized report](report.json) records the operator-selected internal KVM
node, Go 1.26.11, Firecracker/jailer 1.7.0 and the complete compiled source
manifest `be07b967aa22ce8d5205c1087e1eaeab3c122ad8ed8b8de63d831a5b80bd2b6d`
covering 55,406 files. All daemons and guest init used fresh
exact-source build invocations. The race-instrumented suite completed in
673.15 seconds with `-count=1`, nine required subcases and no skips.

| Scenario | Verified behavior |
| --- | --- |
| Admission and ownership | Unready selections are refused, identical recipes reuse an ID, and another key family cannot read the environment |
| npm build and live diagnostics | An isolated builder installs the locked dependency; the HTTP endpoint exposes output before the worker returns; replay after sequence 2 reaches 210 with an explicit retained-tail gap |
| Python build and publication | A hash-pinned binary wheel is built, scanned and imported in a fresh Python 3.13 guest |
| Guest isolation | Separate Node guests import their dependencies, see fresh scratch, run as UID 1000 and return a checksummed artifact and schema-valid result |
| Standard dispatch capacity | A 45-second extended Run completes alongside a standard Run |
| Deadline and cancellation | The actual 300-second budget times out; running cancellation releases dispatch capacity |
| Retirement and collection | New selections stop, admitted work finishes, and owned environment artifacts are collected |
| Durable completion | Each Run's first signed callback receives 503; one delivery stays refused until an actual scheduler restart; all eight ledger deliveries succeed with stable event and delivery IDs |
| Settlement | All eight customer Runs erase payloads and reconcile one usage entry each; pre-teardown and initial/final physical leakchecks pass |

Both environment publications require verified consumed OCI layers and complete
exact-byte Grype scans. Platform Node and builder boot bases use explicit fixture
scans after exact-source guest-init injection; those fixtures do not establish
production platform-image provenance. Customer execution guests remain
networkless. Only the private fixture's daemon processes allow loopback callbacks;
no system service or production egress setting was changed.

The [portable validation](portable-validation.json) covers memory and real
PostgreSQL lease/retention contracts, all eight terminal paths, atomic rollback
when enqueueing fails, duplicate suppression, metadata-only notifications,
scoped API reads and writer rollback, CLI watches and selected Go/Node/Python SDK
contracts. A separate real HTTP dispatcher test exercises 503 retry, dispatcher
replacement, 400 dead-letter retention and manual redrive to 204 without creating
another Run. Schema/SQLC parity, migration compatibility, protocol compatibility,
repository policy checks and changed-code lint pass. Node and Python selected
suites contain eight and ten tests respectively. These are selected feature
checks, not a full repository test run.

The [final Linux lint record](linux-lint.json) reports zero new issues with the
metal build tag for builderd, imaged, vmmdgrpc and the acceptance fixture. Its
source manifest is `1d03d70d01c6a6385454dee5b8695f79087255a0fc6e7b78dd434073bb545640`.
The final tree differs from the VM snapshot in generated filenames for seven
unreleased migrations (SQL bodies unchanged), a host-capacity error classifier
refinement and a wrapped-EOF check, plus documentation/evidence. Fresh-schema
byte parity, PostgreSQL contracts, a focused classifier regression and final
Linux lint cover those changes. No VM lifecycle behavior changed. The portable
record includes exact file hashes and the migration identity mapping.

The [retained attempts](attempts.json) record the preceding fixture type error
and an unchanged 20 GiB builder guard refusal. Both attempts passed final physical
leakchecks. Completed binaries and source copies were preserved with byte/mode
verification to recover disk headroom; shared Go caches were left intact for
these retries. The first Linux lint finding and its correction also remain in
private evidence. Only whitelisted results and evidence hashes are exported;
raw logs, full manifests, receiver details and fixture keys stay private.

Coverage is Node 24/npm and Python 3.13/public binary wheels, cold execution guests
and local artifact storage. The Google-virtualized host provides functional KVM
evidence and does not meet the repository's non-virtualized native production
release gate. Production installer qualification, other runtimes, warm custom
environment restore, remote backends and fleet capacity remain separate gates.
Delivery is at least once: receivers verify signatures and persist event-ID
deduplication with their effects. The hosted Agents resume journey remains open.

See the [usage guide](../../../executions.md#build-diagnostics),
[feature ADR](../../../adr/20261115160000-run-diagnostics-and-completion.md) and
[nine-case acceptance lane](../../run-lifecycle-acceptance.md#five-minute-class-and-dependency-environments).
