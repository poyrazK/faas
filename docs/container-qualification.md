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

This runs image parsing/resolution, image preflight diagnostics, deployment
manifest override tests, raw-ingress deployment selection and TCP/TLS socket
lifecycle tests. It uses test fixtures rather than customer registry
credentials. It verifies contract behavior, not native Firecracker execution.
The gate derives required cases from Go's selected test sources, runs with race
detection, and rejects any skipped test/subtest or missing required pass even
when the test process exits successfully. New image-preflight tests are included
automatically. Its latest local run passed all 187 selected portable contracts.
The command first runs verifier regressions covering test-signature discovery,
external test files, discovery failure, missing passes, skipped subtests, package
failures and nonzero process exits. Test discovery accepts alternative parameter
names and multiline signatures so formatting does not silently drop coverage.
The main CI workflow runs this command in the dedicated
`portable container contracts (strict)` job on its normal pull-request, push and
merge-queue triggers. It also runs the strict portable UDP verifier and retains
`container-contract.log` and `udp-contract.log` even after failure.
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

Startup timeout diagnostics now use the recorded runtime phase: guest-startup
failures explain missing probe responses, and handler-healthcheck failures
explain answered probes without HTTP 2xx or gRPC SERVING readiness. Unknown
phases retain generic guidance for the configured startup deadline and check.
The explanation no longer assumes every timeout concerns `/healthz` or a fixed
35-second deadline. Phase selection only uses known markers and does not copy
raw error text into guidance. Race-enabled whycopy and scheduler regressions
passed, including persisted deployment guidance, and changed-code lint passed.
Native phase behavior remains subject to the qualification requirements above.

UDP route admission now checks durable listener ID and public port as well as
app/account, named guest port and enabled intent. A regression deletes and
recreates a listener and verifies stale routes neither select running instances
nor wake parked apps, while the replacement route remains usable. The full
UDP package passed race testing, changed-code lint passed, and the strict UDP
gate passed all 43 portable contracts. This covers admission against stale
socket identity; native reassignment and cleanup acceptance remains required.

TCP durable routes also carry the listener row ID through TLS negotiation.
A real socket regression pauses certificate lookup during the handshake, deletes
and recreates otherwise identical listener intent, then verifies the stale
connection closes without instance admission, connection credit is released,
and a fresh trusted TLS connection to the replacement succeeds. Five
race-enabled repetitions passed, the full TCP/public-gateway package race tests
passed, and changed-code lint passed after correcting the test's wrapped-error
timeout check. Native listener replacement qualification remains outstanding.

Raw TCP and UDP customer-session selection now excludes running mirror-mode
instances. These instances remain reserved for shadow traffic with its separate
metering and lifecycle contract. Resolver regressions cover mixed customer and
mirror pools and mirror-only pools, where normal scheduler admission is used.
TCP, UDP and public-gateway race tests passed, changed-code lint passed, and
the strict UDP gate passed all 44 portable contracts. Deployment traffic-weight
alignment and native rollout/recovery qualification still require review and
evidence before claiming complete raw-ingress release qualification.

Raw-ingress deployment traffic alignment is now implemented under ADR-391.
New TCP connections and UDP peers select positive-weight live deployments,
filter running instances to the chosen deployment, and pin cold admission to
that exact deployment. Invalid weight sets and scheduler substitution fail
closed. Existing sessions retain their instance. Shared selector tests cover
80/20 bucket coverage, order stability, zero/superseded/foreign rows, malformed
weights and read failure. TCP/UDP resolver regressions cover warm and cold paths;
real UDP/gRPC replacement and TCP socket E2E tests passed after seeding fixtures
with live deployment intent. TCP/UDP/public-gateway race tests and changed-code
lint passed. The strict UDP gate passed 48 portable contracts, including the
shared deployment selector. The native E2E suite cross-compiled for Linux/amd64.
Native weighted canary/rollback, cold-bucket wake and recovery runs remain
outstanding; portable evidence does not qualify them.

Portable rollout socket coverage now switches durable traffic from a warm stable
deployment to a cold candidate and back. It verifies exactly one cold admission,
no additional warm/rollback admission, and continued deployment pinning for
connections established before each traffic change. Five race-enabled runs
passed. The strict container gate now discovers all TCP/TLS tests plus shared
deployment selection tests and passed 187 required portable contracts; the
strict UDP gate passed 48. Both run in the portable container CI job with
retained logs. The checks job now renders the UDP deployment contract and runs
UDP/TLS alert tests; local render/alert checks passed. Workflow structure
validation passed. Full actionlint retains the same nine pre-existing ShellCheck
findings as the unmodified workflow, with no new findings. No remote CI execution
or native rollout qualification is claimed.

## Review delivery

The standalone startup-diagnostics change is published as draft
[PR #3950](https://github.com/poyrazK/faas/pull/3950), head `cd20da7a8`,
based on upstream `f2893f798`. It contains only the explanation catalog and
its whycopy/scheduler regressions. Local race checks and changed-code lint
passed against that base. GitHub CI completed successfully, including unit and
PostgreSQL shards, E2E, lint/build, SDK checks and Go/Actions CodeQL. Conditional
image-build jobs were skipped because this PR does not change runtime images.
These remote results qualify this diagnostics PR; they do not validate the
remaining container runtime, UDP, TLS or deployment-routing changes on this
work branch. Native image, lifecycle, performance, recovery and leak evidence
remains outstanding.

### Isolated OCI primary-identity review — 2026-10-01

Draft [PR #3954](https://github.com/poyrazK/faas/pull/3954) isolates OCI
user/group preservation and image-local primary credential resolution from
cgroup placement and health timing. Base `f2893f798736f803a842bcdd8f3e7a0d3d3e7d45`,
head `2a296d74381722c87c53abc06432c58ee132fc9b`; clean review worktree
`/tmp/gregale-container-identity-20261001`. The isolated decision is ADR-385
because latest main already uses ADR-384 for internal service ports.

Portable OCI/identity race suites pass; the Linux/amd64 guest test binary
cross-compiles; portable and Linux guest scoped lint both report zero issues.
Logs: `/tmp/gregale-identity-portable.log`, `/tmp/gregale-identity-compile.log`,
`/tmp/gregale-identity-lint.log`, `/tmp/gregale-identity-guest-lint.log`.
Remote checks have started and are pending; no remote green claim is made.
Native process execution, preparation/restore and leak qualification remain
unexecuted without a designated Linux/amd64 KVM host. No persistent disks or
ARM64 support are included, and neither review PR has been merged.

### Isolated atomic cgroup launch review — 2026-10-01

Draft [PR #3955](https://github.com/poyrazK/faas/pull/3955) independently isolates
main/companion clone3 placement and rooted control-file writes. Base
`f2893f798736f803a842bcdd8f3e7a0d3d3e7d45`, head
`880fe9438` (short commit); worktree `/tmp/gregale-container-cgroup-20261001`.
The isolated decision is ADR-386, reserving ADR-385 for the separate identity PR.
The Linux/amd64 metal-tagged guest test binary cross-compiles and scoped Linux
guest lint reports zero issues. Logs: `/tmp/gregale-cgroup-isolated-compile.log`
and `/tmp/gregale-cgroup-isolated-lint.log`.

Current CI's pure-Go light shard enumerates `go list ./...` and does not exclude
`guest/init`; therefore ordinary Linux guest regressions are scheduled there.
The explicitly delegated cgroup acceptance test is metal-tagged and requires
`FAAS_TEST_CGROUP_PARENT`; it is not proven by ordinary CI. No Linux execution,
VM/OOM/restore or leak acceptance claim is made yet. PR #3954 currently has four
successful checks, one skipped and one neutral result, with nineteen checks
still running and no reported failure. Neither new PR has been merged.

### Health runtime stack and CLI projection correction — 2026-10-01

Draft [PR #3956](https://github.com/poyrazK/faas/pull/3956) adds main-image
health polling to companion orchestration and carries startup gating, scoped
environment callbacks, effective PORT/PATH/working directory, credentials and
atomic cgroup placement. Its five-file diff is stacked on #3955, now based on
#3954; the decision is ADR-387. These isolated ADR numbers differ from the
older implementation-branch numbering. Linux/amd64 metal-tagged guest tests
cross-compile and scoped lint reports zero issues. Evidence:
`/tmp/gregale-health-runtime-compile.log`, `/tmp/gregale-health-runtime-lint.log`.
Native execution and integrated reporting remain pending.

Remote #3954 daemon-shard execution exposed an old doctor-image expectation
that reduced `1000:1000` to `1000`. The assertion now requires preservation
of the complete OCI identity. The affected CLI doctor race regressions pass
(`/tmp/gregale-identity-doctor-regression.log`); correction commit `4b1febd8f`
was pushed and both downstream branches rebased to include it. Remote checks
on these updated heads are pending. Earlier failed-head evidence remains in
`/tmp/gregale-identity-daemon-ci.log`; no blanket remote-green claim is made.
The PRs remain drafts and unmerged. No production host or persistent storage
was used.

### Exact image timing review — 2026-10-01

Draft [PR #3957](https://github.com/poyrazK/faas/pull/3957), head `34b8f2a8c`,
is stacked on #3956 and isolates image nanosecond decoding, exact manifest
metadata, deployment-grace precedence, guest startup/retry/grace semantics,
and generated Node/Python models. Its decision is ADR-388. No native fixture
execution is implied by this isolated review.

Full `pkg/api` and `pkg/oci` race suites pass; focused imaged timing/override
race tests pass. Linux/amd64 metal-tagged guest tests cross-compile and portable
and Linux guest scoped lint report zero issues. Both pinned SDK generators
complete; Node SDK builds and Python exact-timing model round-trip passes.
Canonical and embedded OpenAPI match. The Python generator removed handwritten
helpers and modified unrelated scaffold tests; those unrelated outputs were
restored after generation completed. Only timing models/exports are retained.
Evidence: `/tmp/gregale-health-timing-full-api-oci.log`,
`/tmp/gregale-health-timing-portable.log`, `/tmp/gregale-health-timing-compile.log`,
`/tmp/gregale-health-timing-lint.log`, `/tmp/gregale-health-timing-guest-lint.log`,
`/tmp/gregale-health-timing-node-gen.log`, `/tmp/gregale-health-timing-node-build.log`,
`/tmp/gregale-health-timing-python-gen.log`.

Remote checks are pending. Local compilation does not establish Linux process
execution, native VM health reporting before/after restore or leak acceptance.
The stack remains draft and unmerged; stateless economics and Linux/amd64 scope
remain unchanged. At last observation, corrected identity PR #3954 had six
successes, sixteen running checks, one queued, one neutral and one skipped,
with no reported failure on the updated head.

### Aggregate VM memory review — 2026-10-01

Independent draft [PR #3958](https://github.com/poyrazK/faas/pull/3958), head
`793d2361f`, base `f2893f798`, isolates physical guest RAM and early/post-boot
host fences aligned with existing main-plus-companion admission/billing.
Unknown or mismatched companion snapshot logical memory falls back to cold
boot. Workload manifest and inner leaf allocations remain individual.
The isolated decision is ADR-389.

Full portable `pkg/fcvm` race suite passes. A subsequent focused regression also
captures the lease before the VMM to prove its early memory fence receives the
aggregate allocation; physical size, post-boot fence and snapshot decisions
are covered. Scoped lint reports zero issues. Linux/amd64 metal-tagged test
binary cross-compiles. Evidence: `/tmp/gregale-memory-isolated-race.log`,
`/tmp/gregale-memory-isolated-fences.log`, `/tmp/gregale-memory-isolated-lint.log`,
`/tmp/gregale-memory-isolated-compile.log`. Native OOM, VM park/restore and leak
acceptance remain pending; remote checks are pending and the PR is unmerged.
The early-fence regression is also retained in this implementation branch.

### Linux CI guest execution and doctor timing closure — 2026-10-01

Completed pure-Go light shard logs confirm ordinary Linux guest package tests
passed on corrected PR #3954, stacked #3955 and #3956 (guest durations 11.371s,
11.374s and 11.521s respectively). Evidence is retained in
`/tmp/gregale-pr-3954-linux-light.log`, `/tmp/gregale-pr-3955-linux-light.log`,
`/tmp/gregale-pr-3956-linux-light.log`. Package success does not prove every
root-gated test ran, nor does it run metal-tagged delegated cgroup acceptance.
No native VM/OOM/restore/leak qualification is inferred from this evidence.

Timing PR #3957 now includes commit `8bed7065a`: image-doctor JSON retains exact
image timing, text renders subsecond durations and startup cadence, and invalid
image timing receives metadata guidance. The doctor race regressions pass and
scoped CLI lint reports zero issues; logs `/tmp/gregale-health-timing-cli.log`
and `/tmp/gregale-health-timing-cli-lint.log`. These changes were already in the
large implementation branch but were absent from the initial isolated timing
PR; they are now included in its review and trigger fresh remote checks.

At last observation #3955 had only its builder image job outstanding. #3954
still had builder image, lint/build, CodeQL Go and migration jobs running, with
no reported failure. Existing image CI builds multiple architecture artifacts;
this work adds no ARM64 container-service support or qualification scope.

### Formatting repair and isolated scratch gate — 2026-10-01

Health-runtime PR #3956's Linux lint/build job exposed a trailing blank line in
the extracted runtime test. Golangci-lint itself reported zero issues; gofmt
failed. Commit `1610b3639` removes that line, and all changed Go files are now
formatted. Timing #3957 was rebased onto the correction (head `e7a9e34aa`);
its append-only timing-test conflict was resolved retaining all tests and
formatting the result. Remote checks on these updated heads are pending.
Failed-head evidence: `/tmp/gregale-health-runtime-ci-lint.log`.

Independent draft [PR #3959](https://github.com/poyrazK/faas/pull/3959), head
`c2d2554eb`, isolates the real scratch mount fixture and production helper,
plus a strict `make test-companion-scratch-contract` runner and operations note.
The runner requires Linux/x86_64 root and rejects skips/missing results; this
mount contract does not require KVM. Linux/amd64 metal-tagged guest tests
cross-compile, scoped metal-tagged lint is clean, shell syntax/ShellCheck with
sources pass, existing native wrapper/verdict regressions pass, and the guard
rejects this Mac. Evidence: `/tmp/gregale-scratch-isolated-compile.log`,
`/tmp/gregale-scratch-isolated-lint.log`,
`/tmp/gregale-scratch-isolated-host-guard.log`,
`/tmp/gregale-scratch-verdict-regressions.log`.
Actual capacity/isolation/reclamation/teardown execution remains unexecuted
until a designated Linux host is available. No VM or native leak qualification
is inferred; PRs remain drafts and unmerged. The independent runner and note
are also retained in this implementation branch.

### Identity CI completion and customer-only TCP review — 2026-10-01

PR #3954 head `4b1febd8f1279a6428e94c40bb4d0f6c8c5e4aa0` has completed all
remote checks: 26 successes and one conditional skip, no running/queued/failing
checks. It remains draft, mergeable and unmerged against base `f2893f798`.
This validates only the isolated identity change; native VM qualification and
the remaining implementation branch are not proven by its green checks.

Independent draft [PR #3960](https://github.com/poyrazK/faas/pull/3960), head
`e020c3e16`, isolates exclusion of mirror and foreign-app instances from raw
TCP warm routing. Mirror/foreign-only sources require normal admission and
cannot suppress a customer wake. New regression covers repeated warm selection,
all supported customer modes, admission, missing-admitter failure and listener
port preservation. Full portable TCP/public-gateway race suites pass and scoped
TCP lint reports zero issues. Logs `/tmp/gregale-tcp-customer-race.log` and
`/tmp/gregale-tcp-customer-lint.log`. Remote checks are pending.
Weighted deployments, durable listener identity, UDP and TLS remain in the
larger branch and require their own focused reviews. No PR has been merged.

### Completed resource CI and TCP weighted rollout review — 2026-10-01

Cgroup PR #3955 head `3a618cdbc44263d37cb75376f05bf5dd908e95a9` has finished
remote CI with 25 successes and one conditional skip. Aggregate-memory #3958
head `793d2361fecdf0faecc4b79c36ab02c1f7ec3dfe` has finished with 24 successes
and two conditional skips. No checks remain running/queued/failing on those
heads; both remain drafts and unmerged. Native acceptance is still unexecuted.

Stacked draft [PR #3962](https://github.com/poyrazK/faas/pull/3962), head
`ebdbb003d`, based on customer-routing #3960, isolates raw TCP connection-level
serving deployment weights. The reusable selector validates exact total and
eligible rows; cold admission pins the selected deployment and rejects a
mismatched response. The decision is ADR-390 in this isolated review, scoped
to TCP; UDP integration remains in the large branch.

Full TCP/selector/public-gateway race suites pass, including the real-socket
cold-candidate switch, retained connections, warm candidate reuse and rollback.
The portable TCP ingress and session-guard end-to-end tests pass (90.542s),
retaining existing gRPC forwarding composition. Scoped lint, formatting and
whitespace checks pass. Evidence `/tmp/gregale-tcp-rollout-race.log`,
`/tmp/gregale-tcp-rollout-e2e.log`, `/tmp/gregale-tcp-rollout-lint.log`.
Remote checks on #3962 are pending. Native canary/rollback, node loss, cold wakes
and leak acceptance are not established by substituted guest execution.

### Bound TCP socket identity and health runtime CI completion — 2026-10-01

Stacked draft [PR #3963](https://github.com/poyrazK/faas/pull/3963), head
`c973dd091`, based on weighted routing #3962, carries durable listener identity
through projections, target intent revalidation and supervisor replacement.
It additionally closes a newly identified gap: a socket bound for an old row
must not resolve a replacement row before refresh. `Server.BoundRoute` rejects
that mismatch before target selection; supervisor replacement cancels sessions.
The new socket-binding fix and regressions are also integrated here.

Full TCP/public-gateway race suites and portable ingress/session-guard E2Es
pass (70.461s). Stale warm/cold intent, bound-socket refusal, projection and
supervisor recreation pass five race repetitions. Scoped lint, formatting and
whitespace checks pass. The implementation branch's full TCP race suite,
including TLS cases, also passes. Evidence:
`/tmp/gregale-tcp-identity-race.log`, `/tmp/gregale-tcp-identity-recreation.log`,
`/tmp/gregale-tcp-identity-e2e.log`, `/tmp/gregale-tcp-identity-lint.log`,
`/tmp/gregale-tcp-bound-integration-race.log`. Remote #3963 checks are pending.

Corrected health-runtime PR #3956 now has 25 successful checks and one
conditional skip, with no pending or failed checks. Timing #3957 still has
one running check, 25 successes and one conditional skip. These are CI scopes,
not native VM or leak evidence. All PRs remain drafts and unmerged.

### Supervisor-wide capacity review and completed CI — 2026-10-01

Stacked draft [PR #3964](https://github.com/poyrazK/faas/pull/3964), head
`9b78ff96e`, isolates one global TCP connection semaphore per supervisor,
shared across all listener ports. The real-supervisor regression uses different
accounts to distinguish the global cap from account limits, verifies rejection
before target selection/wake, and observes released capacity through a socket.
Full TCP/public-gateway race suites pass; the strengthened supervisor regression
passes five race-enabled repetitions; scoped lint, formatting and whitespace
checks pass. Evidence `/tmp/gregale-tcp-capacity-race.log`,
`/tmp/gregale-tcp-capacity-supervisor.log`, `/tmp/gregale-tcp-capacity-lint.log`.
The same regression is retained in this large branch and passes five race runs
against its existing TLS/shared-cap implementation
(`/tmp/gregale-tcp-capacity-integrated.log`). Remote #3964 checks are pending.
Native load/VM wake/leak acceptance is not inferred.

Scratch #3959 head `c2d2554eb43c09578d1e799400b2ebcc1281d1b7` completed remote
CI with 26 successes and one conditional skip. Customer-routing #3960 head
`e020c3e16cebe712f39ff302641df2ad1af365cf` completed with 24 successes and two
conditional skips. No pending/failing checks were observed on those heads;
actual scratch mount execution still requires a designated Linux host.
All PRs remain drafts and unmerged.

### TLS handshake review and host scope (2026-10-01)

The user confirmed Linux/amd64 remains the container host target, persistent disks are excluded for economic reasons, and no native KVM acceptance host is currently available. Draft PR [#3965](https://github.com/poyrazK/faas/pull/3965), commit `a78b25da2`, isolates the TLS handshake primitive and configuration normalization atop TCP capacity PR #3964. Its full TCP/API race suites passed, five handshake-boundary repetitions passed, and scoped lint found zero issues. The new deadline/connection-closure and certificate-provider error-sanitization regressions are retained here. The primitive alone does not enable listener TLS, domain ownership, certificate storage, or API routes.

Listener identity PR #3963 has an unresolved HTTP runtime-policy E2E failure in CI run `36802514043`, job `110179770203`: the 25% candidate was not observed during a ten-second polling interval. The HTTP picker uses a contiguous 100-request cycle, and the helper sleeps 100ms between requests; insufficient sampling under CI load is a hypothesis, not an established cause. Local reproduction with a disposable PostgreSQL database initially skipped due to incorrect credentials, then failed during daemon startup because Darwin lacks `/proc/self/status` required by the capability declaration check. Neither run validates HTTP routing. No CI rerun or test relaxation was used to conceal the failure.

### Edge certificate-provider review (2026-10-01)

Draft PR [#3966](https://github.com/poyrazK/faas/pull/3966), commit `60b0491b1`, isolates the rooted PEM certificate provider atop handshake PR #3965. Full TCP race tests passed, file-provider tests passed five repetitions, and scoped lint found zero issues. Coverage includes atomic rotation and immutable old snapshots, unsafe directory/file modes, root escape, directory pathname replacement, oversized/missing bundles, cancellation, and a new FIFO lookup rejection regression. The FIFO regression is retained in this integration branch. The isolated provider is not yet wired to listeners and does not automate certificate issuance. TCP weighted rollout PR #3962 has completed all checks with 26 successes and one conditional skip; this does not resolve the separate HTTP E2E failure on #3963.
