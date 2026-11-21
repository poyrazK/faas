# Gregale Run lifecycle acceptance

This private operator lane exercises source and OCI Runs through real
Firecracker VMs on a designated Linux amd64 KVM test host. It is functional
acceptance, separate from signed runtime-release eligibility and from the full
P0.3 rollout gate in the [Run roadmap](../gregale-run-roadmap.md).

The runner is `scripts/ci/run-run-lifecycle-acceptance.sh`. Its source journey
is `pkg/e2etest/runlifecycle/TestSourceRunLifecycleMetal`; the OCI journey uses
the existing `TestImageRunsMetal`, `TestImageRunProcessMetal`, and
`TestInteractiveImageRunMetal` in `cmd/e2e`.

## Scope and remaining gates

The source matrix covers Node 24 standard-profile cold boots: structured
results, stdout/stderr, checksummed artifacts, fresh-guest state, denied metadata
network access, language errors, bounded output, running cancellation,
admission-to-result deadline, expiry while the scheduler is stopped, API restart
replay, scheduler/VM owner restart, payload deletion, and one usage-ledger entry
per Run. Cancellation and owner-restart cases wait for guest-produced output;
restart cases require that execution witness exactly once after settlement.
Terminal Runs must leave vmmd's live inventory within ten seconds. The source
matrix disables idle prepared networks and requires a clean resource leakcheck
while its harness is still running, before harness shutdown can retire guests.
Its crash-recovery profile enables `adoption_record` and `adopt_running_vms`:
version-7 ownership proofs let the successor retire interrupted one-shot guests.
This does not qualify the default quarantine-only profile for unattended
recovery, or the separate `native_process_recovery` driver. Production adoption
still requires the version-7 reader/rollback gates in the
[restart-adoption ADR](../adr/20261017-vmmd-restart-adoption.md#1-journal-record-version-7-the-adoption-record).
The fixture uses an empty sanitized-snapshot catalogue and therefore
exercises cold-boot fallback. It does not claim a successful warm restore.

The OCI matrix covers image caching, image USER/WORKDIR/ENV/PATH, overrides,
exit codes, cancellation after dispatch claim, pipes, PTY, and disconnect
cancellation after guest output. The source cancellation case separately
requires a guest execution witness before cancellation.

The report records the actual virtualization detector output. A GCP VM with
KVM can produce functional evidence; `bare_metal_host` is false. Per
`CLAUDE.md`, this does not satisfy the dedicated non-virtualized native release
requirement. Neither a virtualized nor a bare-metal run of this bounded matrix
sets `p0_3_complete` or `runtime_release_eligibility` true. Other language/profile
cohorts and native startup/residency measurements still need qualification
before widening production gates. The additional cohorts below cover bounded
warm/OOM and template/clone behavior. Profile-image scanning remains ADR-383's
own gate.

## Deployed Report Agent cohort

Set `FAAS_RUN_LIFECYCLE_COHORT=report-agent-hosted` to build the
[Report Agent pilot](../../examples/gregale-run/README.md#deployable-report-agent-pilot)
as a Node 24 function and feed its real realtime consumer group. Use the
designated internal acceptance host with at least 48 GiB of scratch, XFS
reflink support, the installed builder and runtime base assets, and disposable
PostgreSQL tools. In addition to the runner's normal source and output variables,
provide an executable Node 24 path in `FAAS_TEST_REPORT_AGENT_NODE`.
Install from the generated bundle's lockfile with `npm ci` and archive its
`node_modules` directory at the root of a private tarball. Provide the
tarball path and SHA-256 in `FAAS_TEST_REPORT_DEPENDENCIES` and
`FAAS_TEST_REPORT_DEPENDENCIES_SHA256`. The native builder installs the
function's own dependencies from the lockfile. The tarball is for the
host-side TypeScript intake driver.

The same host lock covers daemon startup, the source build, both journeys,
and cleanup. The runner stages private base copies and keeps the Firecracker
jail and application layers on a private reflink filesystem on the acceptance
scratch disk. A per-app allowlist permits the disposable PostgreSQL and API
relay address. The app uses an ephemeral database credential, which keeps
the credential out of VM snapshots. The runner uses a private S3-compatible
provider fixture behind
Gregale's signed URL gateway. The fixture uses a 120-second app request budget and a 240-second
consumer group lease so the gateway has time to wake the app on the designated
host. For each input, the fixture holds the committed dispatch response and then the committed artifact-delivery response across
APID restarts. The real consumer group retries each interrupted invocation.
The verdict checks that those retries retain the original requests and
delivery, settle exactly one user event and one tool result, and preserve one
stored report. The CSV case also checks a second customer's download denial.

The verdict includes deploy latency, time from submission to verified
download, time from inbox publication to the Agent's final reply, Run
wall and CPU time, application residency, retained bytes and provider
operations. Compute estimates apply the repository's overage rate before
allowances. The isolated object fixture measures storage and requests
without generating a provider bill.

Run `FAAS_RUN_LIFECYCLE_COHORT=report-agent-setup` to qualify the starter's
`setup:report` command. It shares the deployed cohort's host requirements.
Provide the Node 24-compatible npm CLI script with
`FAAS_TEST_REPORT_SETUP_NPM`. The runner builds `gregale` from the same
source snapshot and uses the regular deployment path. It reserves the app,
database and private route before timing starts.

The setup cohort times the first verified four-order CSV report, then runs the
command again with the same request UUID. Both commands must download the
report successfully. The verdict requires a single deployment, request,
physical Run and object write, with the same delivery identity and report
digest on both reads.

Use `FAAS_RUN_LIFECYCLE_COHORT=report-agent-recovery` with the same setup
prerequisites to qualify regional report progress and reruns. The fixture
temporarily refuses merge admission with a task budget. It resumes polling
from a live customer cursor, lifts the budget, then drops the first accepted
rerun acknowledgement. The retry and replay use the same child request UUID.
The verdict checks both original regional output fingerprints, a single new
merge Run, an immutable parent receipt, and identical private downloads.

Use `FAAS_RUN_LIFECYCLE_COHORT=report-agent-background` to qualify background
regional delivery. The live scheduler sends an account completion webhook to
the generated backend through the private fixture listener. The fixture uses
real Run microVMs and a separate application database. The regional backend
doesn't require an Agent inbox function deployment. A Node process
exits after the artifact API commits the copy and before it can acknowledge
the webhook. The scheduler retries the same delivery into a fresh receiver.
The test then replays the original signed request and verifies the same report
receipt, one private provider write, three original Runs and a signed download.
The verdict records elapsed time from pipeline completion to copy commit
observation and download readiness. The customer API is idle between
submission and verification.

## Warm restore and OOM cohort

Set `FAAS_RUN_LIFECYCLE_COHORT=warm-oom` with a fresh output directory to run
`TestSourceWarmRestoreOOMMetal` instead of the baseline source/OCI matrix.
This cohort targets Node 24 standard at 256 MiB RAM and a 64 MiB scratch shape.
It runs guest resume/attribution regressions before starting real VMs.

The fixture uses the production template publisher with a neutral platform
account. vmmd cold-boots a new template, waits for the pristine execution and
resume listeners, checks the actual backing pins, captures without an Execute
RPC and retires the guest. Publication verifies all four native objects before
inserting the durable catalog row. Schedd stays running. Customer requests,
including unique input sentinels, are created after publication. The historical
`7c4f28da` report predates this producer and describes its manual registration
fixture; the retained source reference reproduces that earlier procedure.

Every warm Run must have a native `wake ok` witness with `method=restore`.
Repeated restores check clean scratch, differing random bytes, UID 1000,
networkless operation and clock recovery after a six-second park. Corrupting
memory and vmstate separately must produce successful same-runtime cold boots.
These are sequential clone checks, not concurrency or startup benchmarks.

Cold and restored OOM cases allocate and touch buffers inside the bounded VM.
The receipt must report `out_of_memory`; the trusted guest diagnostic must
show an increased kernel OOM counter. SIGKILL alone, an ordinary exit,
unavailable counters and regressed counters do not qualify as OOM. The unit
helper must actually execute after dropping privileges. A fresh restored Run
after OOM must remain clean. Both OOM Runs must retire, erase payloads and
produce one usage entry. Language-heap aborts are outside this kernel-OOM
cohort. The same pre-teardown and initial/final leakchecks apply.

Execution guests start their resume listener before the template-ready
marker. The pristine fence closes before decoding caller bytes, and a later
resume request is rejected. The report retains the virtualization limitation
and never marks P0.3 or runtime release eligibility complete.

## Production template and concurrent-clone cohort

Set `FAAS_RUN_LIFECYCLE_COHORT=template-clones` with a fresh output directory.
This selects `TestSourceTemplateClonesMetal` and reruns the entire warm/OOM
cohort against the new producer. The verdict requires both roots and every
subcase, publication/commit-race regressions, vmmd authorization and execution
fences, startup configuration validation, GC retention, and both physical
leakcheck exits. Missing or skipped checks cannot pass.

The template fixture enables schedd's actual background worker before startup.
It waits for that worker's durable publication, checks reuse after scheduler
restart, rejects a wrong kernel pin, and exercises the catalog-aware orphan
collector. Four customer Runs then execute together. While all four are live,
the test checks distinct jail UIDs, root and writable-drive inodes, distinct
capped cgroups and installed seccomp. Guest results require separate scratch,
distinct random bytes and loopback-only interfaces. Cancelling one of a second
pair must preserve its peer. Drive and backing corruption must cold-fallback;
the original four-object digest must remain unchanged afterward. These checks
qualify four same-node, networkless clones; they do not benchmark latency or
prove cross-node/CPU compatibility.

The [2026-11-13 internal-node report](evidence/20261113-run-template-clones/README.md)
records the passing nine-case template/clone matrix and seven-case warm/OOM
rerun, with native restore witnesses and physical cleanup checks.

Production warmup is off unless `FAAS_EXECUTION_TEMPLATE_CAPTURE=1`. It also
requires an existing dedicated, active platform Scale account in
`FAAS_EXECUTION_TEMPLATE_ACCOUNT_ID`, the existing approved runtime artifact
pins, and explicit `FAAS_EXECUTION_TEMPLATE_SHAPES`, for example:

```json
[{"runtime":"node24","profile":"standard","memory_mb":256,"ephemeral_disk_mb":64}]
```

Deploy the additive Firecracker-version migration and catalog-aware imaged
readers before enabling publication; then deploy the new schedd/vmmd readers
and RPC. An older imaged must not remain able to reclaim the reserved platform
namespace. See the [capture/publication ADR](../adr/20261113180000-run-template-capture-publication.md).
Running this private acceptance lane does not deploy or enable production
services. Remote storage, production image provenance and release eligibility
remain separate gates.

The producer requires a local backend and vmmd's existing root-owned private
`native_runtime_snapshot_root`. Schedd and imaged must use its matching absolute
path through `FAAS_NATIVE_RUNTIME_SNAPSHOT_ROOT`; schedd reads the artifacts and
imaged asks vmmd to retire them. The native-process producer additionally requires
the flags and reader gates in the native recovery cohort below. Catalogued
retired templates remain retained until a separate
release-retention policy authorizes deletion.

## Stored files and standalone contracts cohort

Set `FAAS_RUN_LIFECYCLE_COHORT=files-contracts` with a fresh evidence directory.
This selects `TestSourceFilesContractsMetal` and builds the exact-source CLI,
daemons and guest init. The eight required cases exercise stateless preparation,
missing-file refusal before admission, immutable uploads and key-family access,
a 12 MiB cold input/output round trip with a valid result contract, two live
warm clones with independently writable input copies, an invalid contract with
its original result and admission replay, CLI upload/download, and replay after
input revocation and apid restart. Settlement checks payload erasure, one usage
entry per Run and physical cleanup before harness shutdown. Both outer
leakchecks must also pass. Missing or skipped cases cannot qualify the cohort.

The fixture alone enables the v2 writer. This lane does not enable an installed
service or extend the 30-second Run deadline. File inputs, outputs and scratch
remain inside each disposable VM's writable block device. A base source archive
plus a verified delta records both the base commit and every delta file's hash;
the report never labels the unchanged base as the complete tested source.

## Preparation

Use an explicitly selected, idle acceptance host with root access, readable
`/dev/kvm`, cgroups v2, `/etc/faas/builder-acceptance-host`, Firecracker/jailer,
the guest kernel, ext4 tools, networking tools, GCC, and PostgreSQL tools. The
runner refuses active fleet services or existing Firecracker workloads. It
holds `/var/lock/faas-builder-acceptance.lock` through preparation, execution,
and final leakcheck. It does not stop or reconfigure production services.

Provide `/etc/faas/e2e-acceptance.env` as root-owned mode 0600, containing the
dedicated acceptance database URL under `FAAS_E2E_DATABASE_URL`. The runner
rejects `FAAS_SKIP_PG_TESTS`. It uses pgtest's isolated schemas, requiring
`CREATE` on the dedicated database and no migration ledger in its public
schema; it does not require the cluster-wide `CREATEDB` privilege. Use the
private local artifact stores for both lanes. The runner does not source the
host's shared storage configuration or publish test bases into that store.
Never pass provider credentials or the database URL on the command line.

Stage an exact-source Git archive and record its commit, parent/tree when it
is an unpublished worktree snapshot, archive SHA-256, and inclusion manifest.
Do not label a dirty-worktree archive as the unchanged parent commit. The
archive contains `.faas-run-lifecycle-source-sha` with its actual snapshot
commit. Source transfer to a remote host requires authorization for that
destination. This workflow publishes no branch or production release.

Select the compiler pinned by `go.mod`'s `toolchain` directive (or its `go`
directive when no compiler pin exists); the runner verifies the version and
records the executable SHA-256. It uses the host's shared acceptance Go caches,
builds fresh daemon/helper binaries, and compiles guest-init from that source.
It copies the host's real Node 24 base into private staging and replaces only
that copy's `/sbin/init` (resolving the supported `/usr/sbin` parent layout),
with byte-for-byte readback and filesystem checks. It never patches
the host's shared runtime image. The source harness uses private local storage,
fresh age/HMAC keys, a disposable catalogue, and an execution-only marker layer.
Its scan sidecar is explicitly a **test admission fixture**, not vulnerability
scan evidence. Guest init, kernel, and staged base hashes are logged.

For OCI Runs, the runner exports the host's minimal userland as a private OCI
fixture and lets imaged stage it and inject the exact-source guest init. The
builder startup fixture has inert tool files because these tests never launch
a source builder. This qualifies OCI Run process behavior, not source builds,
production image provenance, or vulnerability scanning. Fixture hashes are
retained. Daemon waiters detect unreaped Linux exits without consuming their
exit status, and ignore subscriptions older than the newly launched imaged.
Readiness requires both the daemon's `/readyz` gate and its subscription.

Two runtime corrections emerged from this lane: failed guest protocol results
containing JSON `null` must retain their failure codes when converted to
receipts, and OCI image preparation must stage the generic boot base before
publishing a job image as ready on an empty store. Both have focused regression
tests; the OCI change reuses the existing base producer and policy checks.

## Execution and evidence

Select a fresh staging directory for every attempt. Run under the host's
bounded acceptance supervisor, as root, with:

```sh
FAAS_RUN_LIFECYCLE_SOURCE_SHA="$SOURCE_SNAPSHOT_COMMIT" \
FAAS_RUN_LIFECYCLE_GO="$PINNED_GO_EXECUTABLE" \
FAAS_RUN_LIFECYCLE_OUTPUT="/mnt/e2e-scratch/run-lifecycle-$RUN_ID/evidence" \
bash scripts/ci/run-run-lifecycle-acceptance.sh
```

The default host fixtures are Firecracker 1.7.0 and
`/srv/fc/base/vmlinux-6.1.134`; override `FAAS_TEST_FC_VERSION` and
`FAAS_TEST_KERNEL` together when testing a different selected combination.
This is a correctness lane and sets `FAAS_TEST_REFERENCE_SSD=0`.

The source and OCI suites run with `-race`, `-count=1`, bounded test timeouts,
and real Go JSON output. The evidence checker requires every named case and
both package passes, rejects any skip/failure, and requires zero exits from
both suites and initial/final leakchecks. An empty or partial run cannot pass.
Raw logs, process exits, host identity, source/toolchain metadata, and artifact
hashes remain in the private evidence directory. Keep raw logs private and
review them before publishing a summary.

The source fixture's keys, input images, daemon configuration and ownership
journals live under the operator-owned work tree. They survive test teardown;
only a successful final physical leakcheck permits the runner to remove them.

Failed tests still run final leakcheck. A clean retirement removes only the
owned work directory. A failed final leakcheck retains staging with
`RECOVERY_REQUIRED`; investigate under the same acceptance lock before another
gate. Do not reap unknown processes, mounts, loops or namespaces. The runner
does not repair unrelated host resources.

Portable validation of the lane:

```sh
bash -n scripts/ci/run-run-lifecycle-acceptance.sh
shellcheck scripts/ci/run-run-lifecycle-acceptance.sh
PYTHONDONTWRITEBYTECODE=1 python3 scripts/ci/run-lifecycle-verdict_test.py
go test -tags metal -run '^$' ./pkg/e2etest/runlifecycle
```

The last command type-checks the test package without running it and is not
execution evidence. The verdict checker itself has negative tests for missing
coverage, skipped/failed cases, missing interactive coverage, failed process
exits, and virtualized-host classification.

## Native process recovery cohort

Set `FAAS_RUN_LIFECYCLE_COHORT=native-recovery`. The runner enters a private
mount namespace and each fixture mounts its own private jail tmpfs, retaining
it across daemon death. The host's native journal is neither changed nor used
as a fixture. This cohort requires `native_process_recovery`,
`native_launch_owner_records`, `native_runtime_capture`, and private image,
publication and runtime snapshot roots; it does not use version-7 adoption.

It runs the template/clone and warm/OOM roots plus
`TestSourceNativeRecoveryMetal`. The latter kills the actual daemon at five
metal-only capture barriers, tests schedd interruption during capture,
publication reuse, and running-Run recovery without re-execution. Restart must
retire the original physical owner before opening admission. An incomplete
capture never becomes ready; catalog-aware GC decides when unreferenced
objects can be reclaimed. Retained incomplete native journals are audit
records, not replay authority.

The [native-template recovery ADR](../adr/20261114010000-run-native-template-recovery.md)
defines the new nested authority version and reader/rollback gate. This lane
continues to report functional evidence only on a virtualized internal node;
it never enables production services or grants runtime release eligibility.
Use the [release preflight and rollback checks](run-release-preflight.md)
with the reviewed deployment plan before enabling production capture.

The [2026-11-14 evidence](evidence/20261114-run-native-recovery/README.md)
records all 26 scenarios and 27 customer Runs on the internal test node.
Each native wake must have its original version-2 KVM-only device receipt:
KVM API 12 under the jail credentials, no TUN mount or access, and closed
producer inputs. Supporting regressions cover concurrent image inventory,
reconnection within existing deadlines and acknowledgement only after stop
completion. The full matrix uses race instrumentation and rejects skipped cases.

Allow headroom separately for Go linking and sandbox fixtures. `GOTMPDIR`
can select a pre-created, root-private build directory on another local disk;
the runner's `TMPDIR` and explicit fixture root keep sandbox data in its
acceptance stage. The recorded run used this separation after a compiler-only
scratch exhaustion. Completed source copies may be compressed only with full
content/mode verification and an archive receipt; evidence and shared caches
remain retained. This changes storage placement, not qualification coverage.

## Five-minute class and dependency environments

Set `FAAS_RUN_LIFECYCLE_COHORT=extended-environments` with a fresh output
directory. `TestSourceExtendedEnvironmentMetal` requires all nine named
subcases without skips: recipe reuse and ownership; isolated npm build and
publication; hash-pinned Python wheel build and import; independent Node guests
with artifacts and result validation; a 45-second extended Run alongside a
standard Run; the actual 300-second deadline and running cancellation;
retirement with an admitted Run followed by collection; signed completion retries
surviving a real scheduler restart; and payload, usage and
physical-resource settlement.

The builder uses the production VMM driver. imaged must retain the verified OCI
chain, consumed-layer producer evidence and a complete real Grype scan before
publishing either custom environment. The Node and builder platform boot images
use explicit fixture scans after injecting the exact-source guest init. Those
fixtures are not evidence of production platform-image provenance. The custom
environment scans do not use fixture verdicts. The fixture starts gateway DNS
and resolves the host's actual outward interface for builder egress; customer
execution guests remain networkless.

The npm case observes `build_output` through the scoped HTTP diagnostics endpoint
before the builder worker returns, then resumes the retained cursor without
repeating events. Completion qualification subscribes an account receiver,
verifies HMAC signatures, refuses each Run's first callback with 503 and requires
stable event and delivery IDs on retry. One delivery stays refused until an
actual `RestartSchedd`, after which all eight deliveries must settle successfully.
Only this private fixture enables the existing development-only loopback egress
option so the scheduler can call the in-process receiver. No system service,
customer guest egress policy or production URL validation is changed.

The selected pins and test cases live in
`pkg/e2etest/runlifecycle/environments_metal_test.go`. The suite uses Node 24 and
Python 3.13, public `is-number` and `six` dependencies, local artifact storage,
standard memory/scratch limits and cold boots. It does not qualify all package
combinations, other runtimes, warm custom-environment restoration, remote
artifact backends, production installer releases or fleet capacity. Database,
protocol, reader-first rollout and native release gates remain separate.

Keep at least the builder's unchanged 20 GiB free-space requirement available
before each build, in addition to retained images and scanning work. A separate
root-private `GOTMPDIR` avoids keeping the race-test linker workspace on the
sandbox disk. Do not reduce the build guard or bypass scanning to fit a host.
Preserve failed-attempt evidence and require its final physical leakcheck to
pass before another attempt. New environment and extended-class writers stay
off outside this private fixture.

The [2026-11-15 evidence](evidence/20261115-run-extended-environments/README.md)
records the earlier eight-case baseline, two real environment publications, eight
customer Runs and zero-leak cleanup. Google virtualization remains a functional
qualification limit.

The [diagnostics/completion verification](evidence/20261115-run-diagnostics-completion/README.md)
records the expanded nine-case lane: live output before builder completion,
durable cursor replay and eight signed callbacks retrying across an actual
scheduler restart. All nine cases and both physical leakchecks pass. Its report
also records final Linux metal lint and the exact scope of post-snapshot
failure-path and migration-identity validation.

## Task continuation and customer controls

Set `FAAS_RUN_LIFECYCLE_COHORT=tasks` for the immutable task and durable Agent
continuation journey. Set it to `controls` for the same journey with customer
attribution and admission policies. Both require a fresh output directory and
the same designated idle host, exact-source build and physical leak checks.

`TestSourceRunControlsContinuationMetal` publishes a Node 24 report task with
input/result contracts and a stored artifact. An owned Agent endpoint is bound
to a customer. With schedd stopped, the first invocation reserves capacity;
another call receives structured customer and task budget refusals without
creating a terminal inbox result. The test disables the customer policy,
retires the task, disables task writers and restarts apid before replaying the
admitted call. After schedd restarts, the original Run must complete, validate
its result and deliver exactly one durable inbox continuation. Fresh readers
must fetch its receipt and artifact. A second apid restart and replay must keep
one Run and one inbox record.

The controls cohort also requires settled customer and task usage with one
invocation, zero active slots, zero reserved compute and positive compute used.
Filtered history must retain the authenticated customer/agent attribution and
valid result-contract status. The final witness records two budget refusals.
Portable PostgreSQL and memory-store contracts cover atomic concurrent
admission, key rotation, raw-source bypass attempts, threshold delivery,
missing-ledger conservatism, UTC rollover and the old admission-writer fence.

These are functional KVM checks on the selected internal node. They do not
qualify native production runtime eligibility, customer model invocation,
Sandbox/OCI budgets or token spending. Controls and task preview writers remain
off outside the private fixture. See [Run controls](../run-controls.md) for the
customer contract and migration/rollback order.

The [2026-11-16 controls evidence](evidence/20261116-run-controls/README.md)
records the passing exact-source journey, five Linux lint groups and retained
attempt history. Private snapshot roots must use real directories; vmmd's
symlink rejection remains part of the qualification boundary.
