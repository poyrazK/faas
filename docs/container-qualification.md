# Container qualification

Use this procedure to record what an integrated release has actually passed.
Offline contract tests, native lifecycle acceptance, performance measurements,
and recovery drills are separate evidence. A skipped test or successful image
metadata inspection must never count as a deployed-container acceptance pass.

## Offline contract checks

Run on any supported development machine:

```sh
make test-container-contract
```

This runs image parsing/resolution, image preflight diagnostics, and deployment
manifest override tests. It uses test fixtures rather than customer registry
credentials. It verifies contract behavior, not native Firecracker execution.
The gate derives required cases from Go's selected test sources, runs with race
detection, and rejects any skipped test/subtest or missing required pass even
when the test process exits successfully. New image-preflight tests are included
automatically. Its latest local run passed all 148 selected portable contracts.
The command first runs verifier regressions covering test-signature discovery,
external test files, discovery failure, missing passes, skipped subtests, package
failures and nonzero process exits. Test discovery accepts alternative parameter
names and multiline signatures so formatting does not silently drop coverage.
The main CI workflow runs this command in the dedicated
`portable container contracts (strict)` job on its normal pull-request, push and
merge-queue triggers, and retains `container-contract.log` even after failure.
Workflow wiring is locally validated; no remote CI result is claimed until the
changed branch runs there.

Local runner verification passed `scripts/ci/native-e2e-phases_test.sh` (nine
phases, two lane definitions) and `scripts/ci/run-native-e2e_test.sh`. These
synthetic wrapper checks verify selection and verdict behavior without operating
a KVM host. They do not supply native test or leak-check evidence.

For the portable UDP path, run `make udp-contract-check`. This includes deployment
template and alert checks, then race-enabled tests for framing, socket ownership,
peer quotas, target selection, gRPC forwarding, in-memory intents, authenticated
API routes and CLI commands. Required cases are derived from their source files;
missing passes and any skips fail the gate. Use `make udp-postgres-check` separately
against an isolated PostgreSQL cluster. Neither portable gate replaces the native
UDP lifecycle and leak acceptance below.
The latest strict listener-gate run passed all 42 portable contracts, including
a bare `apps:read` key that can read UDP listeners/TLS status but cannot mutate
either protocol's intent. Stored intent remains unchanged after the denied
requests. This regression also passed independently with race detection and
changed-code lint. A gate attempt failed during API-test linking from local disk
exhaustion; the retry passed after task-owned cache cleanup.

The latest local integration sweep also passed the full `pkg/tcpd`,
`cmd/gatewayd-public`, `pkg/udpd` and `pkg/gateway` test packages with race
detection after the TLS readiness and stalled-handshake changes. This is portable
package evidence, not native lifecycle, performance or recovery qualification.
The full `pkg/api`, `pkg/vmmdgrpc` and VMMD protobuf test packages also passed
with race detection. An initial attempt failed during linking due to local disk
exhaustion; the retry passed after removing old task-owned build-cache outputs.
The full portable `pkg/fcvm` suite passed all 570 top-level tests with race
detection and no skipped tests/subtests. Metal-tagged Firecracker execution and
host leak acceptance are separate and remain unexecuted.
The full `pkg/imaged` suite passed all 420 top-level tests with race detection
and no skipped tests/subtests after supplying `mkfs.ext4`, `debugfs` and the
repository-pinned Syft 1.0.0 (`FAAS_RUN_SYFT_TESTS=1`). The initial default run
passed 418 cases and skipped those two tool-dependent contracts. Real ext4
assembly validation and scanner subprocess execution remain distinct from guest
boot and native lifecycle evidence.
The full `pkg/releaseinstall` package also passed with race detection. The UDP
bridge is included in the shared daemon build list, Packer helper list and
atomic release support catalog. This checks local packaging/installer contracts;
no signed release publication or native installation is claimed.

Generated Node and Python SDKs now include UDP listener operations, TCP TLS
policy/status and exact healthcheck timing models. Local validation passed the
Node TypeScript build and 49 unit/post-processing tests, plus 72 Python tests
excluding the fake-API smoke suite and the opt-in regeneration tripwire. A
separate regeneration/hash comparison was identical across all 3,613 generated
source files. Python wire tests preserve typed enable/TLS PATCH bodies and
unknown certificate status without inventing expiry. The pinned Python generator
needs a scoped adaptation for the PATCH schema's required-field `oneOf`; the
canonical OpenAPI document and server still enforce exactly one mutation.
The existing fake-API smoke suites also passed all 16 Node and 17 Python cases
without skips. A new Node HTTP contract test exercises the generated listener
methods, checking bearer authentication, exact URLs and PATCH/UDP request bodies,
disabled UDP creation and unknown TLS evidence without expiry. It passes and is
included in the regular Node unit command. These fixtures validate SDK transport
contracts, not production listener behavior.
The Go SDK coverage gate (`make sdk-check`) also passes after adding the five
new listener route-to-method mappings. Existing warnings for helper methods
without spec routes remain non-fatal. Changed-code lint for the coverage command
passes; coverage establishes typed-method presence, not runtime execution.
The Python opt-in regeneration tripwire now hashes working-tree generated source
contents rather than Git index entries, so regeneration changes and new models
cannot be hidden by an unchanged index. Its two-regeneration run passed without
skips. Separate regressions verify content-change detection and that the scoped
PATCH adaptation preserves the canonical spec and rejects changed constraints.
After those changes, the full default Python suite passed all 91 tests, including
the fake-API smoke cases; only the separately passed opt-in regeneration test
was deselected. Generator and changed-test Ruff checks pass after removing an
unused generator variable.

## Linux guest process contracts

On a Linux host as root with an explicitly delegated cgroup v2 parent:

```sh
FAAS_TEST_CGROUP_PARENT=/path/to/delegated/cgroup make test-container-guest-contract
```

The parent must expose memory and CPU controllers to child leaves. The test
creates and removes only its own temporary child and never changes parent
membership or delegation. It checks actual UID/GID transitions, creation inside
the cgroup, immediate descendant membership, and exec-probe placement. Runtime poller tests cover start gating, image PATH,
working directory, scoped deployment environment, and refreshed secrets. Missing
controllers fail the test. A private mount-namespace subprocess also checks actual
companion tmpfs capacity through the production mount helper, ENOSPC at the
configured ceiling, independent writable mounts, space reclamation and cleanup.
It also requires `nosuid`, `nodev` and sticky world-writable `/tmp` permissions.
This supplements native microVM acceptance; it does
not replace it. Cross-compilation is not execution evidence.
The scratch contract cross-compiles for Linux/amd64 and passes changed-code lint;
its mount/exhaustion behavior has not yet executed on a Linux acceptance host.

## Native container CI lane

The `e2e-native` workflow accepts `lane=containers`. It first executes the Linux
guest process contracts against the host cgroup v2 hierarchy, requiring every
source-derived contract test to pass. It uses the existing
dedicated acceptance-node staging, host lock, daemon cleanup, and log upload.
The lane derives tests from the direct OCI full-rootfs, port, autoscaling,
process identity/working-directory, healthcheck, restore-hook, checkpoint-hook,
main-image exec-healthcheck context and resumed polling (with and without a
companion), TCP and UDP ingress, streaming, and security
fixture files. New tests in those files become required automatically.
Every selected test must pass; skipped top-level tests or subtests fail the
qualification verdict. The native runner also applies this verdict locally,
so a successful remote process cannot conceal incomplete coverage.

This lane does not prove the separate companion, multi-node recovery, image
matrix, or full platform performance gates below. Retain its uploaded log
with the release commit before claiming native qualification.

## Native lifecycle acceptance

Run on a dedicated native x86_64 Linux acceptance host with KVM and the fixture
configuration required by the tests. Use disposable acceptance workloads.

```sh
RUN_REGEX='^(TestDeployWakeMetal|TestSourceDeployWakeMetal|TestMetalTwoRestoresDistinctUUID|TestMetalSidecarBoot|TestMetalSidecarPortReachable|TestMetalTwoSidecarsColdBoot|TestMetalSidecarOOMIsolation|TestMetalInitialBurstConcurrentRestores)$' make test-metal
make leakcheck
```

The dedicated `run-native-metal-smoke.sh` runner also executes the full fcvm
metal package and derives its required companion tests from
`pkg/fcvm/sidecar_metal_test.go`. Every derived companion test must report
PASS; missing fixtures, skips, and missing results fail its verdict. This is
a separate gate from the cmd/e2e container lane, whose fixtures and coverage
differ. Retain both logs for companion-qualified releases.

Inspect the output for skips. A green command with missing fixture inputs is
incomplete evidence. Record the exact release commit, Firecracker/kernel
versions, image digests, host/fleet configuration, test output, and cleanup
result. These existing gates cover selected deploy/wake, restore identity,
companion, and burst behavior; they do not qualify every portable image shape.

## Image qualification matrix

The following is the required coverage plan, not a claim of completed runs.
Use digest-pinned images and retain a result for each cell. For every image,
verify cold boot, listener/readiness, logs, effective user/working directory,
termination, and redeployment. Request/service images additionally need restore
and lifecycle-callback coverage; workers/jobs need completion, retry, and drain
coverage appropriate to their execution mode.

| Image shape | Compatibility concern |
|---|---|
| Static Go/Rust binary in a minimal root | No shell dependency introduced by process launch |
| Alpine/musl and Debian/glibc servers | Filesystem and userspace portability |
| Node and Python servers | Entrypoint/command and environment behavior |
| JVM server | Initialization deadline and resource pressure |
| nginx/reverse proxy | Readiness, custom stop signal, graceful drain |
| Named non-root user | UID/GID resolution and writable-path permissions |
| Multiple TCP and UDP declarations | Main-port override, named routes, UDP boundary |
| Main application with primary-ingress companion | Startup ordering, readiness withdrawal, rollout agreement |
| Private-registry image and multi-platform index | Credentials, immutable resolution, selected architecture |

## Load and recovery evidence

Use the existing [snapshot performance gate](ops/snapshot-restore-performance.md)
for its exact measurement interval, sample counts, runtime cohorts, and SSD
requirements. Retain failed attempts as well as successful restores. Report
cold fallback separately from snapshot restore, and public-edge latency
separately from platform wake latency.

Additional integrated qualification must cover concurrent same-app wakes,
many-app bursts within admitted capacity, stale external connections, secret
rotation around restore, failed callbacks, and missing/corrupt snapshots.
Multi-node runs must cover node loss during boot and serving, scheduler
restart, interrupted rollout/rollback, and stale resource/port lease cleanup.

Record each scenario as pass, fail, or not run, with an evidence location.
Native M9 recovery uses `make native-m9-acceptance` and its explicit host guards;
do not substitute a local or nested-virtualization run. Outstanding qualification
remains outstanding until executed evidence is available.

## Outstanding implementation and evidence

The container lane is executable coverage, not proof that native runs passed.
The broader improvement scope still includes representative image qualification,
loaded wake/performance and failure recovery, companion resource contention,
and customer-facing diagnostics across runtime stages. OCI user/group identity now shares one image-local resolver across main launch,
probes, companions, and app tasks. Portable tests cover named/numeric primary
groups and root confinement; native process acceptance now uses distinct
UID/GID. Linux guest test execution and native qualification are still required;
compilation alone does not establish those outcomes. Configured main and
companion processes now enter their cgroup atomically at creation, and placement
errors fail startup. Linux syscall acceptance and native companion/OOM/restore
runs remain required before claiming resource-isolation qualification.
The main-image OCI healthcheck poller now runs in companion deployments as well
as single-image deployments, uses scoped deployment environment/secrets, and
waits for main-process startup before polling. Its Linux runtime and native
vsock-reporting evidence remain outstanding. Image healthcheck durations now
retain Docker nanosecond precision through parsing, manifest preparation, and
runtime polling, including StartInterval. Regenerate older deployment artifacts
to pick up corrected image metadata. Native probe-context tests now require a
new observation after restore, rather than accepting a snapshot-carried count.

Raw TCP socket binding is implemented behind the opt-in public-edge rollout
switch and covered by `TestTCPIngressMetal` in the container lane. Native execution
and rollout evidence remain required. UDP forwarding is now implemented behind
its own opt-in/source-CIDR gate. `TestUDPIngressMetal` belongs to the deploy phase
and container lane; it requires cold-boot and restore wake records, isolated
peers, empty/full-size binary datagrams, disabled endpoints and teardown. It has
not run on native KVM yet. PostgreSQL 16 store/migration passes, local socket race
tests and Linux compilation do not substitute for this acceptance. Per-listener
TLS termination now has durable API/CLI intent, verified app-domain ownership,
an opt-in file provider, bounded handshakes, certificate rotation and readiness
metrics/alerts. Local trusted socket composition tests pass. Portable TLS
shutdown evidence also covers a stalled client that sends no
ClientHello. Server cancellation closes it promptly, causes no workload
admission and releases global/account credits. The socket regression passed five
race-enabled repetitions and changed-code lint; this does not qualify native
drain or node-failure behavior. `TestTCPIngressMetal`
now includes a TLS policy transition, wrong-SNI rejection observed through public
edge metrics without a new instance wake, trusted echo after park, live-session
closure on disable and kernel listener release. This expanded test cross-compiles
for Linux/amd64 and passes changed-code lint; native execution is still pending.
Customer-facing per-edge certificate status now has authenticated API and CLI
projections with local race coverage; native end-to-end qualification remains
pending. The native TCP test also requires the customer status endpoint to expose
the real edge's observation and exact provisioned certificate expiry, then to
return unknown status without expiry after disable. This test cross-compiles for
Linux/amd64 and passes changed-code lint and native phase-contract checks; it has
not executed on a KVM host.
Native TLS rotation coverage now atomically replaces a complete PEM bundle,
requires a trusted new handshake to present the replacement certificate, verifies
that the established session still echoes with its original negotiated identity,
checks the updated customer expiry and closes both sessions on disable. This
expanded case cross-compiles for Linux/amd64 and passes changed-code lint; native
rotation acceptance remains unexecuted.
The native TLS case also replaces the bundle with an expired certificate and
then removes it. Each state must publish `not_ready` without expiry, reject a
valid-SNI client even with client verification disabled, release the active
session and leave durable wake identities unchanged. Reprovisioning a valid
bundle must recover readiness before the trusted restore wake. The expanded
fixture cross-compiles for Linux/amd64 and passes changed-code lint; these native
failure/recovery scenarios remain unexecuted.
Persistent container disks are outside the improvement scope. Gregale's stateless
contract and storage economics remain governed by [ADR-158](adr/158-ephemeral-disk-boundary.md):
durable application state belongs in object storage or an external database.
OCI `VOLUME` declarations must not provision durable disks or imply a persistence
guarantee. This improvement scope targets the existing Linux/amd64 service;
additional CPU architectures are out of scope.

Ephemeral storage qualification remains required. Use disposable workloads to
verify the plan's total logical filesystem ceiling, exhaustion behavior, cleanup
after termination and failed startup, and isolation between instances restored
from the same snapshot. Cover main writable storage and companion `/tmp` scratch
separately; `/tmp` is memory-backed and must respect resource limits. Record cold
fallback and redeployment behavior without treating snapshot-carried filesystem
contents as durable application state. Preserve shared-base layering and the
existing quota source of truth throughout these checks.

Companion VM memory now matches admission: main RAM plus positive companion
allocations, with host overhead added once. Unknown or mismatched companion
snapshot memory lengths cold boot. The native OOM fixture uses retained
anonymous memory and requires a SIGKILL result before checking main-service
survival; reclaimable file reads or transport failures no longer count as an
OOM-isolation pass. Execution of this strengthened gate remains outstanding.

## Integration with upstream main (2026-10-01)

Merged upstream main at `a50543f7c` after preserving the container work in a
local checkpoint. Regenerated protobufs, sqlc bindings and both SDKs from the
merged sources. Container ADRs now use numbers 384–390 to avoid upstream
number collisions.

Post-merge verification passed all 148 portable container contracts and all
42 portable UDP contracts, including race checks and strict skip rejection.
The first UDP build exhausted local disk space; the rerun passed. Task-owned
Go cache cleanup recovered space for subsequent verification. Node build and
65 unit checks passed, as did 42 selected Python tests (the opt-in regeneration
test was deselected). SDK route coverage and native E2E runner contract checks
passed. The merged metal E2E suite cross-compiled for Linux/amd64. This remains
compilation evidence; no native KVM qualification was performed.
