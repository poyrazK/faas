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

### Durable TLS intent review and integrated SQL rejection (2026-10-01)

Draft PR [#3967](https://github.com/poyrazK/faas/pull/3967), commit `abf83895b`, adds normalized listener TLS intent, an append-only migration, matching schema snapshot/sqlc model, and atomic disable-on-policy-change atop provider PR #3966. Memory and real PostgreSQL TCP-listener race tests passed with no skips. Direct SQL and store-API invalid-policy attempts were rejected without changing an enabled listener; missing-listener updates report not found. Scoped state lint found zero issues and sqlc regeneration left the generated model unchanged. The new SQL/no-mutation regression is retained here, where both PostgreSQL TLS tests also passed with no skips (`/tmp/gregale-tls-intent-integrated.log`). This does not yet expose customer TLS controls or wire the edge; domain ownership must still be enforced by those layers.

The isolated file-provider package also cross-compiled for Linux/amd64 (`/tmp/gregale-tls-certificates-linux.test`); this is compilation evidence, not native execution. Build-cache exhaustion interrupted the first integrated SQL run. Task-owned cache cleanup recovered disk space, and the authoritative rerun completed successfully. No repository source or evidence logs were discarded.

### Replay-safe container migrations after CI finding (2026-10-01)

The first CI migration run for intent PR #3967 failed `TestNewMigrationsAreReplaySafe`: bare column/constraint additions could not tolerate applied DDL with missing ledger rows. Commit `c29c1396f` corrects only this unmerged migration using `ADD COLUMN IF NOT EXISTS` and constraint guards bound to the current listener table. Its exact replay gate passed against real PostgreSQL, the two PostgreSQL TLS store regressions passed again, and `make migrations-check` passed. The draft description now records this correction; replacement CI is pending.

The same finding applied to the still-unmerged observation migration in this integration branch. Its table/index creation and table removal now tolerate already-existing/absent objects. The repository replay gate was run with the complete new container migration set `20260930193000001,20260930220000001,20261001120000001`; it passed without skips (`/tmp/gregale-container-all-migrations-replay.log`). Previously merged migrations were unchanged. The prepared API extraction worktree `/tmp/gregale-container-tls-api-20261001` is clean at corrected intent commit `c29c1396f`; customer API/runtime/SDK integration remains to be reviewed and qualified.

### TLS API and generated-client extraction, unpublished pending edge enforcement (2026-10-01)

Local commit `1fec0dadc` on `codex/container-tls-api-20261001` at `/tmp/gregale-container-tls-api-20261001` adds customer TLS intent, verified app-owned domain validation, disabled termination creation, separate policy/enable mutations, canonical/embedded OpenAPI changes, and matching generated Node/Python contracts. It is deliberately unpublished: public-edge enforcement must enter its dependency chain before an API draft is published. The runtime API cannot safely be delivered independently of the edge checking TLS intent and current ownership before admission.

Focused API/state race tests and schema parity passed; the TLS API test passed three race repetitions without skips, and scoped Go lint found zero issues. OpenAPI lint passed with repository warnings. Node build and real HTTP SDK serialization tests passed; the Python real HTTP test passed. Both generators ran twice after the commit and left their generated trees unchanged. The pinned Python generator needed a narrowly guarded adaptation for required-field-only `oneOf`, preserving the typed update request while leaving the canonical schema and server mutation checks intact.

The expanded API rejection cases, domain ownership table, and Node/Python HTTP serialization tests are retained in this integration branch. Its focused API/domain race tests passed with no skips, as did Node/Python HTTP tests. The Node unit-test command includes the new transport regression. These results prove customer-intent validation and SDK transport, not public TLS termination, certificate readiness, native VM lifecycle, or release. The separate HTTP runtime-policy CI failure and native acceptance remain unresolved.

### Public-edge TLS enforcement and dependent API review (2026-10-01)

Draft [#3969](https://github.com/poyrazK/faas/pull/3969), commit `54b83bbd4`, projects TLS hostname into routes, binds sockets to that intent, reserves credits before negotiation, and selects/admit workloads only after TLS success. Target resolution re-reads listener TLS policy and verified app-wide domain ownership, rejecting revocation, foreign apps, environment scope, or changed policy before warm instance lookup or cold admission. Supervised listeners receive the file provider, and the public gateway opens it from absolute `FAAS_TCPD_TLS_CERT_DIR` with startup/shutdown cleanup. Full TCP/public-gateway race suites passed and scoped lint found zero issues. Real trusted supervised TLS sockets and warm/cold rejection cases passed; the new regressions also passed three repetitions in this integration branch. Admission and forwarding are substituted, not native VM execution.

The API/SDK extraction was rebased onto this runtime, becoming commit `47128e8af`, and published as dependent draft [#3970](https://github.com/poyrazK/faas/pull/3970). Combined API/TCP/public-gateway TLS, listener, supervisor, and schema-parity race checks passed after rebase. Its earlier real HTTP SDK tests and twice-generated stable client trees remain applicable because runtime rebasing did not change the canonical schema or SDK inputs. The previous unpublished-state record is historical; the draft now explicitly depends on edge enforcement. Both drafts remain unmerged. Issuance automation, readiness observations/status, native acceptance, other remaining container work, and the separate HTTP runtime-policy CI failure remain unresolved.

### Versioned certificate-evidence storage and environment-contract correction (2026-10-01)

Draft [#3971](https://github.com/poyrazK/faas/pull/3971), commit `1c50026fa`, isolates per-edge certificate observations atop the API draft. Publication requires the current enabled terminating listener, matching hostname/intent timestamp, and a strictly newer per-edge observation. Status rejects disabled, stale, changed, invalid, or future evidence; pruning and listener deletion remove retained records. The record contains no key material, provider paths, or provider errors and cannot establish fleet coverage, public reachability, client trust, issuance, or guest readiness. Edge publication and customer status exposure are still separate work.

Memory and real PostgreSQL observation race tests passed with no skips, including monotonic replacement, stale intent, multiple edges, pruning, deletion, expiry/freshness status, and 128-byte UTF-8 identifier boundaries. The new observation migration passed the repository replay gate against PostgreSQL; scoped lint found zero issues and sqlc regeneration produced no drift. The new ASCII/multibyte/invalid-UTF-8/control boundary regression is retained here and passed three race repetitions in the full integration branch.

Broader CI on runtime #3969 and API #3970 caught an extraction omission: `FAAS_TCPD_TLS_CERT_DIR` was read but absent from the daemon environment registry and generated catalog. Runtime commit `267911d61` adds the optional defaulted override declaration and catalog row; all environment-contract tests passed locally. API #3970 was rebased to `e99e73156`, and observation #3971 includes that correction. Replacement CI is pending. The declaration/catalog already existed in this integration branch. The separate HTTP runtime-policy E2E failure and Linux/amd64 native KVM acceptance remain unresolved.

### Edge certificate publication and composed TLS review (2026-10-01)

Draft [#3972](https://github.com/poyrazK/faas/pull/3972), commit `ab7ebeeac`, isolates supervisor certificate evidence publication and aggregate readiness/expiry metrics atop observation storage #3971. Publication retries failed writes, tolerates stale-intent conflicts, bounds writes with a two-second context, heartbeats unchanged evidence every 15 seconds, and prunes expired records. Shutdown clears aggregate metrics. Certificate evidence does not establish fleet availability, client trust, or guest readiness.

Full TCP and public-gateway race suites passed. Certificate observation/publication tests and the composed real-socket provisioning, rotation, observation, metrics, admission, and disable test passed three repetitions; scoped lint reported zero issues. Scheduler/guest execution remain substituted. Runtime/API/observation replacement CI had no failures at the latest snapshot, with checks still running. Customer status extraction, issuance/operator integration, remaining container review, the separate HTTP runtime-policy failure, and native Linux/amd64 KVM acceptance remain outstanding.

### Customer certificate status and clients review (2026-10-01)

Draft [#3974](https://github.com/poyrazK/faas/pull/3974), commit `e7d59854a`, isolates authenticated per-edge TLS certificate status, matching canonical/embedded OpenAPI, generated Node/Python clients, Go client, CLI TLS policy/status commands, and operator guidance atop publication #3972. Ownership is checked before storage access; storage errors are sanitized. Stale and disabled intent project unknown status without expiry. Responses use observed_edges scope and do not establish fleet coverage or guest readiness.

API/schema and CLI race checks passed; status and CLI cases passed three repetitions. Scoped Go lint reported zero issues. OpenAPI lint passed with existing repository warnings. Node build and Node/Python real HTTP transport tests passed, and both generators ran twice after commit with no generated drift. Logs are `/tmp/gregale-tls-status-{go,repeated,lint,node-http,python-http,node-determinism,python-determinism}.log`. The integration branch retains the corrected CLI fallback usage and Go client documentation. API/SDK #3970 completed CI with 26 successes and one conditional skip; other dependency checks remained live at the snapshot. No drafts are merged. Issuance/operator integration, remaining container review, separate HTTP rollout failure investigation, and native Linux/amd64 KVM acceptance remain outstanding.

### Safe TCP TLS deployment and certificate alerts review (2026-10-01)

Draft [#3975](https://github.com/poyrazK/faas/pull/3975), commit `ea11f6649`, adds optional Ansible certificate-directory wiring, availability/expiry alerts with runbook metadata, and CI-executed local deployment/alert fixtures atop status #3974. The prepared implementation previously reset arbitrary configured-directory ownership and permissions; the role now validates existing real root-owned directories without changing them and creates only missing directories as root:faas 0750. It rejects controls/dot components/filesystem root and quotes the systemd environment path. Issuance and atomic PEM installation remain operator responsibilities; native deployment is unqualified.

The isolated and integrated Ansible fixtures passed with 80 successful tasks, zero changes, and eleven intentional guard rejections. Existing directory ownership/mode were preserved; unsafe paths, unsafe existing directories, empty configuration, and quoted rendering were exercised. Promtool rule syntax/scenarios, alert metadata, production bootstrap syntax, and daemon/Ansible/environment contract race tests passed. Workflow structure passed actionlint with ShellCheck disabled; full actionlint reported exactly the base workflow's existing ShellCheck finding codes/text. Logs use `/tmp/gregale-tls-operations-{deployment,integrated,alerts,alert-metadata,ansible-syntax,contract,workflow}.log`. Native creation/service convergence and KVM acceptance remain unexecuted.

CI on status #3974 failed the Go SDK coverage gate because the extraction omitted its route-to-method map entry despite including the typed method. Commit `1e097332b` registers the existing method; make sdk-check passed in the status draft, the rebased operations draft, and this integration branch. Replacement CI is pending. Runtime #3969, observation storage #3971, and publication #3972 completed with respectively 25/26/24 successful checks and 2/1/2 conditional skips. No drafts are merged. The separate HTTP rollout E2E failure, still-live capacity CI jobs, remaining container review, and native Linux/amd64 acceptance remain outstanding.

### Weighted HTTP rollout probe margin and failure diagnostics (2026-10-01)

Independent draft [#3976](https://github.com/poyrazK/faas/pull/3976), commit `df43b4237`, addresses an observation weakness found during the separate #3963 HTTP E2E investigation. The gateway's 100-slot weighted stride can select 75 stable requests before a 25% candidate. The probe's former 100ms pause consumed 7.5 seconds of its ten-second window before request latency or policy propagation. Successful-probe pauses are now 10ms, errors still back off 100ms, and failures report attempts and successful captured picks. The deadline, exact instance assertion, routing implementation, and traffic-share assertions are unchanged.

Gateway weighted/deployment picker race tests passed, E2E package compilation passed, and scoped lint reported zero issues (`/tmp/gregale-gateway-rollout-{picker,compile,lint}.log`). The fixture cannot execute its daemon path on Darwin because capability validation requires /proc/self/status; full Linux daemon E2E execution awaits draft CI. The old failure lacked attempt counts, so its exact cause remains unproven and it is not marked resolved. The updated probe is retained here. Native KVM qualification remains separate.

### UDP intent storage review and generated SQL conversion (2026-10-01)

Independent draft [#3977](https://github.com/poyrazK/faas/pull/3977), commit `d8ac1ddea`, isolates app-owned UDP intent, memory/PostgreSQL stores, reserved independent UDP port namespace, optional interface, migration/schema, and generated queries. Production UDP SQL is now entirely sqlc-generated, retaining the app ownership lock and transaction, public binding filters, and conflict semantics. Public-port validation before int32 conversion prevents oversized values from aliasing an existing listener. Direct SQL invalid updates cannot change enabled intent. The integration branch retains the query conversion and new regressions in commit `0ccec1316`. Public API/edge/VMMD transport review and native acceptance remain separate.

Integrated memory/PostgreSQL race tests passed without skips, scoped lint found zero issues, and the strict PostgreSQL gate passed all four required cases, including migration constraints. The isolated store race tests also passed without skips; migration/default/constraint and repository replay checks passed against PostgreSQL, migration inventory checks passed, and lint reported zero issues. Post-commit sqlc regeneration left both generated trees unchanged. Logs use `/tmp/gregale-udp-{sqlc,intent}-*.log`. Initial isolated store linking and lint typechecking failed from local disk exhaustion; after all handles terminated, task-owned Go cache cleanup recovered space and authoritative reruns passed. No source or evidence logs were removed. The owned PostgreSQL cluster was stopped after completion.

Rollout probe draft #3976 completed with 24 successful checks and two conditional skips, including all four Linux daemon E2E shards. Shard assignment puts both runtime-policy and normal-path traffic-split tests in shard 3; its race-enabled, uncached package run succeeded in 204.795 seconds (`/tmp/gregale-gateway-rollout-ci-e2e.log`). The nonverbose log provides package/job evidence rather than individual test verdicts. The old #3963 failure remains historical and its exact cause is unproven. Native KVM lifecycle qualification is not supplied by these daemon fixtures. Operations #3975 completed CI with 25 successes and two conditional skips. Drafts remain unmerged.

### Datagram framing/helper review and cancellation/readiness hardening (2026-10-01)

Independent draft [#3978](https://github.com/poyrazK/faas/pull/3978), commit `73a38d8e7`, isolates bounded four-byte datagram framing, connected UDP/pipe bridging, and the actual helper entrypoint. Zero-length datagrams retain their boundaries; oversized/truncated frames cannot reach the guest. A pre-canceled bridge now closes resources before reading input. Real pipe cancellation tests also cover a blocked reply write and verify input/socket closure. The helper checks descriptor 3 is a writable pipe before opening its UDP socket, avoiding descriptor reuse as an unrelated file or guest socket. Child-process tests invoke the actual helper main for readiness, datagrams/replies, EOF exit, invalid arguments, missing/non-pipe/read-only readiness descriptors, and absence of unintended file/socket writes.

Framing/bridge race tests passed five repetitions; combined socket/helper subprocess race tests passed three repetitions without skips. Scoped lint found zero issues. `/tmp/gregale-vmmd-udp-bridge-linux-amd64` is a stripped static Linux/x86-64 ELF; this is compilation rather than execution evidence. The integration branch retains the hardening, regressions, and adds helper cases to the source-derived strict UDP gate. Verifier regressions passed, and the full integrated gate passed all 54 portable contracts with no skips (`/tmp/gregale-udp-wire-{test,helper,lint,linux-build,verifier,integrated}.log`). Namespace launch, VMMD/gRPC extraction, packaging, native guest lifecycle/leak acceptance, and remaining public UDP integration are separate work.

Broader CI on storage #3977 failed the inherited `TestServiceBindingFetchCompatiblePort/legacy`: its Node subprocess was killed at the five-second process timeout with zero requests and no bad-port result (`/tmp/gregale-udp-intent-ci-pure.log`). UDP store tests are not implicated by that failure, but CI remains unresolved pending investigation. Status #3974 completed with 26 successful checks and one conditional skip. Drafts remain unmerged and native Linux/amd64 KVM acceptance remains unexecuted.

### Bounded namespace bridge readiness (2026-10-01)

Independent draft [#3980](https://github.com/poyrazK/faas/pull/3980), commit `cc2d16b1b`, bounds the existing TCP launcher readiness record to 4,096 bytes and its wait to 35 seconds, preserving the helper's existing 30-second guest dial allowance. ADR-402 records the shared TCP/UDP contract; the integration branch applies the reader to their shared launcher. Caller cancellation and deadlines close the real pipe to interrupt an otherwise silent helper. Oversized records, including records without a newline, cannot grow the reader without bound. Error readiness now kills the helper before reaping it instead of trusting it to exit. Successful readiness still proves socket setup only.

Real-pipe race tests passed five repetitions in both isolated and integration trees: normal/error/exact-limit records, oversized/truncated records, pre-cancellation, cancellation of a silent live pipe, deadline expiry, and descriptor closure. Final scoped lint reports zero issues in both trees. The integrated strict UDP gate passed all 57 source-selected portable contracts without skips; the source inventory now includes these readiness cases. Verifier regressions passed all four tests. Final integrated VMMD builds as static Linux/amd64 (`/tmp/gregale-vmmd-readiness-linux-amd64`). Evidence is in `/tmp/gregale-bridge-readiness-{tests,isolated-tests,lint,isolated-lint,gate,verifier,linux}.log`. An initial lint check rejected an unchecked deferred Close return; this was corrected and scoped race tests/lint rerun. No native helper namespace, KVM lifecycle or leak execution is claimed.

The inherited Node Fetch fixture from #3977 passed five uncached local race repetitions with both legacy bad-port rejection and canonical request assertions (`/tmp/gregale-fetch-reproduction.log`). This does not establish the cause of its Linux CI timeout. Storage #3977 completed CI with 24 successes, one conditional skip and that failure; it remains unresolved. No blind rerun was performed. Drafts remain unmerged; stateless guest storage and Linux/amd64 scope remain unchanged.

### Admitted-peer UDP transport review and flow-control shutdown (2026-10-01)

Draft [#3981](https://github.com/poyrazK/faas/pull/3981), commit `86cc00b88`, isolates VMMD/gateway datagram transport, protobuf schema/generated bindings, independent directional caps, default peer idle expiry, helper release/Packer inventories and environment declaration. It is based on helper #3978 and includes readiness foundation #3980 (`84f7f51c6`). ADR-403 describes the transport boundary: public ownership/admission/CIDR/rate/session controls remain mandatory and are separate review work. Stateless guest storage and Linux/amd64 scope are unchanged.

VMMD no longer reports failed UDP operations as successes; the named terminal error reaches its existing operation counter. Initial transport failures retain their status instead of being rewritten as invalid initialization. Actual registered gRPC tests cover empty/datagram-first/missing-instance/invalid-port/negative-cap initialization, missing live namespace, EOF before init, deadline while awaiting init and error metrics. Direct handler regressions retain Canceled/DeadlineExceeded/Unavailable receive errors.

The original synchronous reply pump could keep the handler blocked in gRPC Send after a request failed. Both frame directions now run concurrently, and either can terminate the handler. Pipe/process cleanup remains owned by its caller; gRPC cancels outstanding Send/Recv when the handler returns. A real HTTP/2 fixture uses the production duplex pump with guest pipes, establishes actual reply backpressure by observing a stable send count during Send, then sends a repeated init. The handler returns InvalidArgument and the blocked sender is released by gRPC. This regression passed five race repetitions (`/tmp/gregale-udp-transport-flow-control.log`); it is portable transport evidence, not privileged namespace/process evidence.

Final isolated transport/gateway/protobuf/readiness race tests passed three repetitions without skips. Both final scoped lint checks found zero issues. Environment declaration/catalog checks and post-commit protobuf regeneration/check passed and the isolated tree remained clean. VMMD, public gateway and UDP helper compile as stripped static Linux/amd64 ELF binaries in `/tmp/gregale-udp-transport-linux/`; no native execution is claimed. The final integrated strict UDP gate passed all 61 source-selected contracts without skips. Logs are `/tmp/gregale-udp-transport-{isolated-tests,isolated-lint,integrated-lint,env,proto,proto-check,linux,integrated-gate}.log`.

Initial isolated typechecking identified two HTTP test clients needing the added RPC method; narrow Unimplemented compatibility methods were added and checks rerun. An initial deadline regression incorrectly expected Header to return an error; gRPC may expose empty headers before the terminal status, so the test now asserts Recv status and completed handler metrics. Before final checks all build/test/lint handles were terminal, and clearing the task-owned Go cache recovered local disk space. No unrelated checkout/cache or evidence artifact was removed. All local handles are terminal. Drafts remain unmerged; native namespace, VM lifecycle, node-loss, contention and leak acceptance remain unexecuted.

### Atomic durable UDP reservation ceiling (2026-10-01)

Draft [#3983](https://github.com/poyrazK/faas/pull/3983), commit `ec256a2c7`, stacks on storage #3977 and bounds all durable UDP reservations per app to the existing 16-port workload contract. ADR-404 distinguishes this technical bound from financial plan quotas. The existing workload cap now resides in the shared limits table, with the reservation ceiling derived from it. Disabled reservations and reservations retained across manifest changes count; deletion frees a slot. PostgreSQL holds its existing ownership app-row lock across the generated count and insertion; MemStore holds its mutex. A typed limit error carries the ceiling and attempted count. Duplicate-name conflict behavior at capacity is retained.

The integration API maps this error to HTTP 409 and stable `udp_listener_limit`, including limit/observed fields and deletion guidance/documentation URL. Actual authorized HTTP routes reject both automatic and explicit public-port creation at capacity and permit creation after deleting an old reservation; tests model old-manifest reservations with all listeners disabled. The isolated draft includes the problem definition, while public API handlers remain separate review work.

Final memory/PostgreSQL race tests passed three repetitions in both integration and isolated trees without skips. Eight concurrent creators competing for the last slot admitted exactly one; tests also cover disabled retention, deletion recovery, duplicate names and independent capacity for another app. HTTP route regressions passed three race repetitions. Both scoped lint checks found zero issues. Post-commit sqlc regeneration left the isolated generated tree unchanged. The strict PostgreSQL gate passed all five required cases without skips, including the new concurrent reservation test; the full source-selected portable UDP gate passed all 63 cases without skips. Logs are `/tmp/gregale-udp-quota-{state,isolated-state,api,lint,isolated-lint,postgres-gate,contract-gate}.log`. All local handles are terminal and the owned PostgreSQL cluster is stopped.

Public admission, exposure, app-retirement reservation reclamation, account/session/rate controls and native namespace/VM lifecycle/node-loss/leak acceptance remain required. This ceiling alone does not prove them. Guest storage remains stateless, Linux/amd64 is the only target, and drafts remain unmerged.

### Listener restore-window and final-purge lifecycle (2026-10-01)

Draft [#3984](https://github.com/poyrazK/faas/pull/3984), commit `a17d86480`, stacks on #3983 and fixes listener reclamation after the app's existing restore window. PostgreSQL already cascades TCP/UDP reservations on permanent app deletion; MemStore now deletes those children atomically during the claimed, expired purge. The integration tree also removes TCP TLS observations, matching their listener foreign-key cascade. ADR-405 preserves enabled and disabled reservations during the seven-day metadata restore window rather than releasing customer ports prematurely.

The new lifecycle contract exposed TCP binding lookup/feed returning enabled listeners belonging to deleted apps. Both stores now exclude deleted apps and mismatched account ownership from these binding reads, matching UDP's existing behavior. PostgreSQL uses generated queries for both reads and validates public-port bounds before int32 conversion. The integration adapter retains TLS mode/hostname; its retirement fixture verifies these survive restoration and lookup.

Final lifecycle race tests passed three repetitions against both MemStore and real PostgreSQL in integration and isolated trees without skips. They exercise hidden routes and feeds during the restore window, early claim/purge rejection, enabled/disabled reservation retention, restoration of the same identities and ports, final expired/claimed purge, port reuse for both protocols, oversized TCP port rejection and other-app isolation. Both scoped lint checks found zero issues. Post-commit isolated sqlc regeneration left the generated tree unchanged. The strict PostgreSQL gate passed all six required cases, and the full source-selected portable UDP gate passed all 64 contracts without skips. The gate inventories now include retirement wrappers, with the PostgreSQL case excluded from the portable selection. Evidence is in `/tmp/gregale-listener-retirement-{tests,isolated-tests,lint,isolated-lint,postgres,contract}.log`.

Initial lifecycle checks failed on deleted-app TCP binding visibility; those failures led to the binding-query/feed correction. An isolated compile initially lacked the sqlc import in its older TCP file; it was added before final tests and lint. After all local Go/lint handles were terminal, clearing the task-owned Go cache recovered space before final checks; no unrelated cache, checkout or evidence artifact was removed. All local handles are terminal and the owned PostgreSQL cluster is stopped. Live edge reconciliation, native namespace/VM lifecycle, node-loss and leak acceptance remain unexecuted. Public UDP rollout is still pending, Linux/amd64 is the sole target, guest storage remains stateless and drafts remain unmerged.

### Bounded UDP admission primitives and pre-cancellation (2026-10-01)

Draft [#3985](https://github.com/poyrazK/faas/pull/3985), commit `575c333e6`, stacks on helper #3978 and isolates bounded peer queues/payload ownership, reply identity/context, global/account pool admission with idempotent releases, and shared directional account packet/byte ledgers. ADR-406 records the existing technical defaults, bounded idle-eviction cache and caller obligations. The production socket owner must share pools/ledgers across listeners, filter CIDRs before allocation, retain listener identity, bound replies and honor their contexts. These primitives alone do not supply public socket rollout or VM qualification.

An already-canceled caller previously could consume or publish queue data because a context select may choose a concurrently ready channel. Peer Receive/Send now check the caller error before queue operations; NewPeer rejects an already-canceled parent before constructing its queue. Concurrent cancellation after entry retains normal context-select behavior, without claiming strict ordering against simultaneous I/O. The integrated socket loop treats peer-construction cancellation as clean shutdown after releasing its pool slot. Tests preserve queued inbound data after pre-canceled Receive and prove pre-canceled Send never populates a ready reply queue. Pool contention admits exactly the global capacity, respects account limits, and tolerates concurrent duplicate releases even after a new generation has reused its capacity.

The complete isolated peer/rate suite passed five race repetitions without skips, including account packet/byte/empty-datagram budgets, refills, cache capacity/idle eviction and shared account contention. The initial integrated focused run passed peer cases and the cache case five times; its narrower regex did not select the separately named account-rate cases, so it is not claimed as the complete rate suite. The full integrated source-selected UDP gate then passed all 67 contracts without skips, including all rate cases. Both scoped lint checks found zero issues. Evidence is `/tmp/gregale-udp-primitives-{tests,isolated-tests,lint,isolated-lint,contract}.log`. All local handles are terminal. Public socket/supervisor/API review, native load, VM lifecycle and leak qualification remain required. Linux/amd64 and stateless guest storage remain the scope; drafts remain unmerged.

### UDP socket admission and cleanup (2026-10-01)

Draft [#3987](https://github.com/poyrazK/faas/pull/3987), commit `52bb09431`, stacks on transport #3981 and includes the primitive foundation from #3985. ADR-407 isolates immutable listener identity, fail-closed source CIDRs, shared global/account admission and directional rate budgets, bounded peer/reply queues, target validation, reply identity and cancellation cleanup. Serve now closes its owned socket on configuration rejection as well as normal exit; canceled replies are discarded before writing and shutdown deadline errors do not trigger operational callbacks. Real loopback regression tests prove shared account capacity across two sockets, reuse after one listener exits, final slot release, and closure after invalid configuration.

The complete isolated UDP suite and integrated socket cases passed three race repetitions without skips. Both scoped lint checks found zero issues; all 69 integrated source-selected portable UDP contracts passed without skips. The socket test artifact `/tmp/gregale-udp-sockets-linux-amd64.test` compiled to a static Linux/x86-64 ELF; this is compilation, not native execution. Evidence is `/tmp/gregale-udp-sockets-{tests,isolated-tests,lint,isolated-lint,contract,linux}.log`. Initial lint findings (direct error assertion and an unused isolated supervisor helper) were corrected before final checks. All local verification handles are terminal.

Supervisor reconciliation, production public gateway/firewall wiring, customer API review and native namespace/load/VM lifecycle/leak acceptance remain pending. No designated KVM host is available. Linux/amd64 remains the sole target and guest storage remains stateless. Drafts remain unmerged.

### Bounded UDP supervisor reads (2026-10-01)

ADR-408 adds a five-second child deadline to durable UDP intent reads and rejects late/canceled results before socket changes. A failed periodic read retains the last validated socket set for retry; initial failure still rejects startup. Cancellation during the initial read now returns cleanly without readiness or reconciliation-failure metrics. State-source context cooperation remains required; no detached reader goroutine is introduced.

All five supervisor cases passed three race repetitions without skips, including new initial-read cancellation and deadline propagation tests. Scoped lint reported zero issues, and the full source-selected UDP gate passed all 71 portable contracts without skips. Logs are `/tmp/gregale-udp-supervisor-{tests,lint,contract}.log`; all these handles are terminal. The supervisor slice is not yet published as an isolated review draft. Deployed database recovery, production public gateway/firewall integration and native Linux/amd64 KVM lifecycle/load/leak acceptance remain pending. No persistent guest disks are introduced.

### UDP supervisor review and invalid-refresh recovery (2026-10-01)

Draft [#3988](https://github.com/poyrazK/faas/pull/3988), commit `d3eab4917`, stacks on socket #3987 and includes the reviewed storage foundation #3977. The supervisor now has an isolated review branch. A real loopback regression verifies that an invalid refresh preserves the existing serving socket, then a later valid deletion closes it without rebinding. Integrated supervisor tests and the complete isolated UDP suite passed three race repetitions without skips; isolated scoped lint reported zero issues. Evidence is `/tmp/gregale-udp-supervisor-{recovery-tests,isolated-tests,isolated-lint}.log`.

The expanded portable gate did not complete: its final protobuf test-package link failed with `no space left on device` (`/tmp/gregale-udp-supervisor-recovery-contract.log`). It is not counted as a full pass or substituted with the prior 71-case run. All handles launched for this work are terminal. Disk/cache recovery must inspect other local build activity before clearing shared task cache. Target-resolution review, production wiring/firewall integration and native KVM/load/leak acceptance remain pending. Drafts are unmerged, Linux/amd64 is the target and guest storage remains stateless.

### UDP gate recovery and post-wake intent validation (2026-10-01)

Available disk space recovered to 6.9 GiB before verification. Other local Go/lint processes were active, so shared task cache was not cleared. The exact failed expanded gate was rerun after its earlier process was terminal and passed all 72 portable contracts without skips (`/tmp/gregale-udp-supervisor-recovery-contract-final.log`). This supplies the full supervisor gate evidence missing from the preceding disk-exhausted run, before the subsequent target resolver change.

ADR-409 reuses authoritative app/listener/manifest validation after a scheduler wake, preventing a disabled/replaced/deleted listener or changed app from accepting a new peer through stale admission. It does not undo scheduler-owned wakes or claim an atomic transaction with forwarding. All target-resolver cases passed three race repetitions without skips, including a new real MemStore fixture disabling the listener during successful admission; scoped lint found zero issues (`/tmp/gregale-udp-target-revalidation-{tests,lint}.log`). These focused checks qualify the resolver change; the earlier 72-case gate is not claimed as a post-change full run. All handles launched here are terminal. Isolated target review, public production wiring/firewall exposure and native KVM/load/leak acceptance remain pending.

### UDP target cancellation boundary (2026-10-01)

The resolver now rejects nil contexts and checks caller cancellation after listing instances, before selecting a warm target or requesting a wake. The regression uses valid stored app/listener/deployment intent and a source that cancels the caller while returning a routable RUNNING instance; it requires context.Canceled, an empty target and zero scheduler admission calls. This covers sources with buffered successful results rather than assuming every store honors cancellation before returning rows. ADR-409 records the additional boundary.

All resolver cases passed three race repetitions without skips and scoped lint reported zero issues (`/tmp/gregale-udp-target-cancel-{tests,lint}.log`). All handles launched here are terminal. This is focused resolver evidence, not a new full portable gate run. Isolated resolver publication, production wiring, deployed recovery and native KVM/load/leak acceptance remain pending; Linux/amd64 and stateless guest storage remain the scope.

### UDP target admission review (2026-10-01)

Draft [#3989](https://github.com/poyrazK/faas/pull/3989), commit `8e82f5db0`, stacks on supervisor #3988 and isolates the target resolver, shared serving-deployment picker, traffic-weight constant and ADR-409. Ownership/current manifest/maintenance checks precede warm or cold routing; mirror instances are excluded, peer deployment selection is pinned and admission must return the selected deployment. Post-wake intent validation and canceled-instance-read rejection retain the integrated fixes.

The initial isolated compile lacked the picker traffic-weight constant; adding it exposed the older isolated MemStore ignoring maintenance updates. The existing integrated three-line maintenance update was included as a prerequisite; the test was retained. Final complete isolated UDP and deployment-picker suites passed three race repetitions without skips, and scoped lint found zero issues (`/tmp/gregale-udp-targets-isolated-{tests-final,lint-final}.log`). Initial failure evidence remains in `/tmp/gregale-udp-targets-isolated-tests.log`. All handles launched here are terminal. Production gateway/firewall wiring, deployed recovery and native KVM/load/leak acceptance remain pending; Linux/amd64 and stateless guest storage remain the scope. Drafts remain unmerged.

### Public UDP bind configuration validation (2026-10-01)

ADR-410 requires the production UDP bind setting to be an IPv4 literal and validates it before scheduler/VMMD dependency setup, avoiding DNS resolution during intent reconciliation. The default stays 0.0.0.0; source CIDRs remain separately required. The environment contract records the restriction. All public gateway UDP configuration tests passed three race repetitions without skips, and scoped lint reported zero issues (`/tmp/gregale-udp-public-bind-{tests,lint}.log`). All handles launched here are terminal. This is configuration-boundary evidence rather than deployed lifecycle/firewall or native KVM acceptance; isolated public wiring review and those remaining qualifications are pending.

### UDP deployment source and environment boundaries (2026-10-01)

Enabled UDP deployment now validates the bind IPv4 literal and every IPv4 CIDR before rendering configuration; octets must be decimal 0..255 and prefixes 0..32. This closes the previous colon-only source check that could accept malformed or injected firewall entries. The UDP environment template JSON-quotes bind, sources and optional dependency/TLS settings, preventing embedded newlines from creating extra environment assignments. ADR-410 records the deployment boundary.

All five deployment-render contracts passed, including matching runtime/firewall source sets, default-off behavior, identical systemd units, quoted newline/space/quote preservation, and malformed/firewall-injection CIDR rejection. Ansible syntax checking of the actual gateway-public task file passed, and git diff --check passed. No playbook was applied, firewall loaded or service restarted; deployed behavior remains unqualified. Native KVM/load/leak acceptance remains pending; guest storage remains stateless and Linux/amd64 is the target.

### Shared UDP gateway/firewall preflight (2026-10-01)

The nftables role can run independently, so gateway-only validation did not cover its configuration path. Both roles now include one shared UDP policy assertion task; nftables does so before host mutations and the gateway before rendering UDP configuration. All six deployment-render contracts passed, including role include ordering. Actual localhost Ansible assertion-only playbooks accepted valid IPv4 policy and rejected prefix /33, injected firewall text and a hostname bind address, each with the expected return status (`/tmp/gregale-udp-policy-{valid,bad-cidr,injection,hostname}.log`). These playbooks contained only shared assertions: no firewall, package, service or deployment mutation occurred. Public wiring publication and native/deployed acceptance remain pending.

### UDP string opt-in agreement (2026-10-01)

The firewall template previously used raw Jinja truthiness while runtime environment/preflight used Ansible bool conversion; a string false value could therefore open UDP rules with ingress disabled. The firewall now uses the same bool filter. All seven deployment contracts passed, covering supported true/false inventory strings in both runtime and firewall rendering. An actual local Ansible template run rendered the production firewall template to temporary files and asserted no UDP listener rule for string false and an explicit rule for string true (`/tmp/gregale-udp-bool-render.log`); no nftables rules were loaded and no service changed. The process is terminal. Production slice publication and native/deployed acceptance remain pending.

### Isolated UDP deployment review (2026-10-01)

Draft [#3991](https://github.com/poyrazK/faas/pull/3991), commit `dd6da3b10`, stacks on target admission #3989 and isolates opt-in role defaults, quoted UDP environment, shared gateway/firewall preflight, source-filtered UDP rules and canonical/installed unit environment loading. All seven isolated deployment-render contracts passed and diff whitespace checking passed. An initial extraction script stopped at a template marker absent from the older baseline; the missing firewall/unit edits were completed before final verification. Daemon startup wiring is not included in this deployment slice and remains separate review work. Previously recorded integrated actual Ansible policy assertions and temporary renders are not claimed as new isolated execution. No deployment or firewall was applied. All handles launched here are terminal; native/deployed acceptance remains pending.

### Isolated public UDP daemon wiring (2026-10-01)

Draft [#3992](https://github.com/poyrazK/faas/pull/3992), commit `457b37ac9`, stacks on deployment #3991 and wires opt-in UDP startup, scheduler/VMMD clients, supervisor readiness, ordered peer/client shutdown and public metrics registration into gatewayd-public. Raw ingress draining stops UDP before invoking TCP drain. The isolated wiring copies the already integrated bind/source validation and uses the reviewed resolver/supervisor stack.

Isolated UDP configuration tests passed three race repetitions without skips and scoped lint reported zero issues (`/tmp/gregale-udp-public-isolated-{tests,lint}.log`); whitespace checks passed. These cases qualify configuration/opt-in rather than actual dependency startup or deployed recovery. Prior socket, supervisor and stream fixtures remain separately scoped evidence. All handles launched here are terminal. Real PostgreSQL/scheduler startup and recovery, firewall application, native VM lifecycle/load/leak acceptance and customer-facing UDP API review remain pending. No rollout or merge occurred; Linux/amd64 and stateless guest storage remain the scope.

### UDP HTTP foreign-owner mutation rejection (2026-10-01)

The real authenticated UDP API lifecycle fixture now creates an enabled foreign-account listener and attempts enable, disable and delete through the requesting account. Each request returns 404 and the full foreign listener record remains unchanged, complementing existing foreign list/create rejection. The fixture passed three race repetitions without skips (`/tmp/gregale-udp-api-ownership-tests.log`); the launched handle is terminal. Shared bounded JSON decoding already rejects unknown fields and trailing values; no decoder change was needed. Isolated customer API review, broader acceptance and native/deployed qualification remain pending.

### UDP HTTP malformed-request state preservation (2026-10-01)

The real authenticated UDP API fixture now rejects unknown create fields, trailing JSON, out-of-range public/guest ports and null creates, then proves no listener reservations exist. Invalid updates cover missing/null/string enabled values, unknown fields and trailing JSON, then compare the full stored listener record before/after. The lifecycle and quota-problem fixtures passed three race repetitions without skips (`/tmp/gregale-udp-api-validation-tests.log`); the launched handle is terminal and diff whitespace checking passed. No production decoder behavior changed. Customer API isolated publication and native/deployed acceptance remain pending.

### UDP memory-store mutation cancellation (2026-10-01)

Create, enabled-state update and deletion now check caller cancellation before acquiring the memory-store lock and again before mutation. A pre-canceled regression exercises all three operations, requires context.Canceled and verifies the complete existing reservation stays unchanged with no extra create. The full focused MemStore UDP listener suite passed three race repetitions without skips (`/tmp/gregale-udp-store-cancel-tests-final.log`) and scoped lint reported zero issues (`/tmp/gregale-udp-store-cancel-lint.log`). The regression directly covers pre-cancellation; the second under-lock guard is not claimed as a separately exercised contention case.

The initial test link failed with no space left on device. After its test/lint handles were terminal and disk availability recovered to 2.1 GiB, the exact failed test command was rerun successfully. No shared cache was deleted. All launched handles are terminal. Isolated customer API review and native/deployed acceptance remain pending.

### UDP mutation lock-contention cancellation (2026-10-01)

A deterministic context fixture captures the first uncanceled Err result while the store mutex is held, then cancels the caller before releasing the mutex. Table cases exercise create, enabled-state update and delete and require context.Canceled plus unchanged full reservation state. This directly covers the second under-lock guard left unexercised by the preceding pre-cancellation case. Both cancellation fixtures passed three race repetitions without skips and scoped lint reported zero issues (`/tmp/gregale-udp-store-lock-cancel-{tests-final,lint-final}.log`).

Initial test/lint attempts failed from disk exhaustion. After those exact handles were terminal and a process inventory showed no Go/compiler/linker/lint activity, Go's task-specific cache cleanup recovered space; source, evidence artifacts and unrelated caches were preserved. The rerun remained on its original live handle through completion. All handles launched here are terminal. Isolated customer API publication and native/deployed acceptance remain pending.

### Isolated customer UDP listener API review (2026-10-01)

Draft [#3993](https://github.com/poyrazK/faas/pull/3993), commit `cec2a87b2`, stacks on durable reservation #3983 and isolates app-owned list/create/update/delete routes and safe request/response models. Authentication/MFA/scope wrappers protect reads and writes; create retains idempotency, declared-manifest validation, disabled defaults, explicit/automatic public port reservation and typed quota responses.

Isolated API lifecycle/read-only/quota fixtures passed three race repetitions without skips, including foreign enable/disable/delete state preservation and malformed create/update rejection; scoped lint found zero issues (`/tmp/gregale-udp-api-isolated-{tests,lint}.log`). The isolated read-only fixture retains UDP checks and excludes TLS-only dependencies covered separately in the TCP stack; integrated combined scope evidence remains unchanged. All handles launched here are terminal. SDK/CLI/schema review, memory-store cancellation follow-up integration and native/deployed acceptance remain pending. No rollout or merge occurred; Linux/amd64 and stateless guest storage remain the scope.

### Go UDP client HTTP contracts (2026-10-01)

Dedicated real-HTTP Go client fixtures now exercise list/create/update/delete, bearer authentication, escaped app/listener path segments, create request fields, explicit enabled=false encoding, response decoding and 204 deletion. A quota fixture confirms HTTP409 is returned as a typed APIError carrying the UDP listener-limit code. Both client fixtures passed three race repetitions without skips and scoped lint reported zero issues (`/tmp/gregale-udp-client-{tests,lint}.log`). All launched handles are terminal. No client implementation change was required. Client/CLI/schema isolated publication and native/deployed acceptance remain pending.

### Isolated Go UDP client publication (2026-10-01)

Draft [#3994](https://github.com/poyrazK/faas/pull/3994), commit `e110dee0e`, stacks on customer API #3993 and isolates the four Go client methods with real-HTTP contract fixtures. Isolated contracts passed three race repetitions without skips and scoped lint found zero issues (`/tmp/gregale-udp-client-isolated-{tests,lint}.log`). All launched handles are terminal. CLI/schema publication and native/deployed acceptance remain pending; no merge or rollout occurred.

### UDP CLI local port validation (2026-10-01)

The UDP add command now applies the shared workload-port validator and public reservation range before HTTP create. CLI fixtures cover negative/oversized guest ports and both public-range boundaries and prove no requests occur for invalid input, alongside existing routing, create/list/enable/disable/delete and JSON output cases. The real-HTTP CLI fixture passed three race repetitions without skips and scoped lint reported zero issues (`/tmp/gregale-udp-cli-{tests,lint}.log`). All launched handles are terminal. CLI isolated publication, schema/SDK review and native/deployed acceptance remain pending.

### Isolated UDP CLI review (2026-10-01)

Draft [#3995](https://github.com/poyrazK/faas/pull/3995), commit `c9d7607f8`, stacks on Go client #3994 and isolates app/apps UDP listener command dispatch, create/list/enable/disable/delete, text/JSON output and local shared port validation. The isolated real-HTTP CLI fixture passed three race repetitions without skips and scoped lint found zero issues (`/tmp/gregale-udp-cli-isolated-{tests,lint}.log`). All launched handles are terminal. Schema/other SDK publication and native/deployed acceptance remain pending; no rollout or merge occurred.

### UDP OpenAPI response/allocation alignment (2026-10-01)

UDP list/create/update/delete operations now document HTTP403 scope/MFA rejection and HTTP503 capacity/store-unavailable failures returned by their existing wrappers/handlers. Create public_port accepts zero as automatic allocation as well as the explicit 40000..49999 range, matching the implementation's zero sentinel; omission remains allowed. Vacuum schema lint completed successfully with its repository-wide 1,620 warnings and 108 informs (`/tmp/gregale-udp-schema-lint.log`); this is not claimed as warning-free lint. Whitespace checking passed. SDK regeneration/schema isolated publication and native/deployed acceptance remain pending.
