# Five-minute Runs and dependency environments — 2026-11-15

**All eight functional scenarios passed with real Firecracker VMs. New writers
remain off by default; production runtime eligibility remains open.**

The [sanitized report](report.json) records the complete source manifest
`efe3908c5fffca2cba05a2743453d335fc43682680166dce682e65f9d2794daf`
covering 55,366 source files, Go 1.26.11 and the selected internal KVM node.
The base commit identifies the starting tree; the full manifest identifies the
uncommitted source that was actually built. Every daemon and guest init was
rebuilt. Firecracker and jailer used version 1.7.0.

| Scenario | Verified behavior |
| --- | --- |
| Admission and recipe reuse | Unready selections refused, matching recipe reuses its ID, another key family cannot read the environment |
| npm build and publication | `is-number@7.0.0` installed in an isolated builder VM; verified OCI layers, producer proof and complete exact-byte Grype scan required before readiness |
| Python build and import | SHA-256-pinned `six==1.17.0` installed as a binary wheel in a second builder VM, scanned, imported in a fresh Python 3.13 guest at UID 1000 |
| Repeated Node use | Separate guests import the installed package, see fresh scratch, run at UID 1000 and return a checksummed artifact and schema-valid result |
| Reserved standard dispatch | A 45-second extended Run completes while a standard Run also succeeds |
| Deadline and cancellation | An actual 300-second admission-to-result budget times out; another running extended Run cancels and releases capacity; the combined case took 305.47 seconds |
| Retirement and collection | Retirement denies fresh selections, preserves admitted work, then removes every owned artifact and the environment row |
| Settlement | All eight customer Runs erase payloads and reconcile exactly one usage entry each; pre-teardown and initial/final physical leakchecks pass |

The race-instrumented suite completed in 634.34 seconds with `-count=1`, no
skips and zero test/leakcheck exits. Both environment publications retain real
scan and consumed-layer proof while live. Explicit retirement then exercises
their normal physical collection. The Node environment occupied 421,527,552
bytes and Python 247,463,936 bytes across base and layer. These are fixture
observations, not general package-size or build-latency guarantees.

The [portable validation](portable-validation.json) records selected scheduler,
API, builder, image publication, CLI, state, protocol, storage and SDK
regressions; PostgreSQL migration/schema/SQLC checks; schema parity; writer
rollback replay; compatibility checks and changed-code lint. Stored-file v4
and extended/environment v5 both pass a 12 MiB private-protocol round trip.
The acceptance checker has 18 portable negative tests and its runner passes
ShellCheck. This is selected feature coverage, not a full repository test run.
The [Linux lint record](linux-lint.json) also confirms zero new issues for guest
init, the executor, builderd and the metal acceptance fixture, with the metal
build tag and the qualified source manifest.

The [failed attempts](attempts.json) preserve source and evidence hashes plus
zero-leak cleanup for the preceding fixture and disk-space failures. The lane
found a missing platform builder fixture, missing gateway DNS/outward-interface
configuration and insufficient compiler/sandbox disk separation. The successful
run uses a private `GOTMPDIR` on the other disk and retains the unchanged 20 GiB
builder free-space guard. No shared Go cache was cleared. Qualification also
corrected exported-image cleanup/read boundaries and extended workflow
capability preflight, with focused regressions.

Only whitelisted results and hashes are exported. Raw logs and exact-source
manifests remain private on the node. Final documentation, generated protobuf
tool-version comments and the spec-compliance DTO discovery test changed after
the compiled manifest; production behavior did not change.

The host reports Google virtualization, so this is functional KVM evidence and
does not satisfy the repository's non-virtualized native release requirement.
The platform Node and builder boot bases use explicit test-only scan fixtures
after source guest-init injection. The newly built custom environments require
real Grype verdicts. Production installer/runtime image qualification remains
separate. Coverage is Node 24 and Python 3.13, public dependencies, standard
memory/scratch, cold execution guests and local artifact storage. Other
runtimes, warm custom-environment restoration, private registries, installation
scripts, remote artifact backends, fleet capacity and full billing remain open.
No production service or writer was enabled.

Reproduce with `FAAS_RUN_LIFECYCLE_COHORT=extended-environments` in the
[operator guide](../../run-lifecycle-acceptance.md#five-minute-class-and-dependency-environments).
See the [feature ADR](../../../adr/20261115100000-run-extended-class-and-environments.md)
for limits, protocol v5 and reader-first rollout/rollback.
