The running-job journey composes contained commands, a managed HTTP worker,
isolated PostgreSQL and object fixtures, a held checkpoint, an in-place VM
pause/resume, a real job retry and simulated email. Its runner is
`scripts/ci/run-native-rehearsal-acceptance.sh`; its contract is
[ADR-20261018000100](adr/20261018000100-rehearsal-native-running-job-qualification.md).
Native execution of this new journey is pending. All existing admission flags
remain off by default.

The coordinator also supports `--journey postgres-recovery`, specified by
[ADR-20261018000200](adr/20261018000200-rehearsal-postgres-fault-recovery.md).
It uses the alternative PostgreSQL-fault manifest in the same fixture directory.
Enable the preceding command, fixture and worker gates plus
`FAAS_REHEARSAL_POSTGRES_FAULTS=1` on the dedicated acceptance apid. This journey
does not require suspend/resume gates, the metal pause prerequisite or its image
environment variables. Native KVM, matching installed source, the shared lock,
independent app deletion and clean leakchecks remain required.

## Hosted Task fixture and receipt

### Prepare the fixture and check the runner

Prepare the reviewed Node web and worker archives offline with the
[hosted Task rehearsal plan](adr/20261118062934-hosted-task-rehearsal-plan.md). Use
the same source revision as the acceptance installation and a private evidence
root outside the checkout.

```bash
evidence_root=$(mktemp -d /var/tmp/gregale-hosted-task.XXXXXX)
python3 scripts/ci/rehearsal-native.py --journey hosted-task --prepare-only \
  --out "$evidence_root/prepared"
```

The `prepared` directory contains `fixture/web.tar.gz`, `fixture/worker.tar.gz` and
`preparation.json` with the archive and reviewed source tree SHA-256s and the
exact `build_report` invocation. Both archives use the pinned Node starter
and dependency lockfile. The web process accepts Tasks and the dedicated
worker executes them. The worker archive also carries the stock Task
migration command.

Before the owner approves a plan, complete this checklist on the dedicated
acceptance host.

- Follow [Native host setup](#native-host-setup). For this journey, include
  the active local compute node UUID as `node_id` in the root-owned enrollment
  marker. The enrolled node must resolve to `/run/faas/schedd.sock` and
  `/run/faas/vmmd.sock`.
- Stage the dedicated single-box APID configuration, the rehearsal admission
  settings and the disposable PostgreSQL fixture cluster. Confirm that
  `faas-apid` can read the account seal identity, credential lookup policy,
  runtime seal recipient and value-fingerprint key through the installed service
  configuration described in the [hosted Task ADR](adr/20261118062934-hosted-task-rehearsal-plan.md).
- Load the dedicated `FAAS_API` and the approving owner's unscoped personal
  administrator credential in `FAAS_TOKEN` through the normal host credential
  mechanism. Run the preflight as root from the clean installed checkout.

```bash
GREGALE_REHEARSAL_NATIVE_ACCEPTANCE=1 \
  python3 scripts/ci/rehearsal-native.py --journey hosted-task --check-runner
```

The preflight checks native KVM access, required tools, the installed revision,
the enrollment marker and whether the credential resolves to the dedicated
account. The acceptance child checks personal administrator authority and the
local daemon sockets before invoking the private recovery runner.

Use the prepared archives for hosted source review and binding checks. Match
the retained review's web and worker source digests to `source_sha256` in
`preparation.json`, then have the owner approve a fresh recovery plan. Keep the
approved source intent ID and the plan SHA-256 for the native invocation.

### Run hosted Task recovery

The private smoke entry in `pkg/apid/toolregistry` checks the host through
its in-process adapter before consuming the owner's approval. It scopes the
smoke run to the exact contained deployments and their original lease, and
cleans up an owned run even if the smoke is cancelled. Claim replay returns
the reserved run identity.

The first-claim entry is `StartHostedTaskRehearsal`. The operator wires its
adapter with `pkg/tasktoolrehearsal/native.Startup` and checks the enrolled
account, node and platform revision before spending the approval. Supply the
`source_sha256` values from trusted offline preparation. The private entry
compares both with the retained review before claiming the run.

The ordinary deployment pipeline starts the worker. The adapter wakes the
exact web deployment and forwards MCP only to that instance using the
private gateway forwarder. Both deployments stay at zero traffic. The worker
fixture logs a digest of the claim token, and the scheduler attributes the
line to the VM. The restricted observer checks the live lease, Task, worker
registration and deployment in PostgreSQL. The entry then disposes of the
fixture. It returns first-claim evidence and leaves full acceptance to the
recovery run.

Use a new output path under the private evidence root. Set `intent_id` and
`plan_sha256` to the approved 64-character lowercase hexadecimal values.

```bash
GREGALE_REHEARSAL_NATIVE_ACCEPTANCE=1 \
  python3 scripts/ci/rehearsal-native.py --journey hosted-task \
  --out "$evidence_root/native" \
  --hosted-task-intent "$intent_id" --hosted-task-plan "$plan_sha256"
```

The root wrapper acquires the shared native acceptance lock and checks for
leaks before and after the run. It prepares the fixture again from the clean
checkout and passes the four source and archive digests to the one-shot
acceptance child under `faas-apid`. The recovery runner stops the original
worker through the scheduler and observes a replacement lease. The wrapper
publishes `hosted-task-receipt.json` after receipt validation, the final leak
check and a second host identity check.

### Verify and retain the evidence

Check the runner's `qualification.json` for `status: passed`,
`scope: hosted-task-journey`, and the expected platform commit, account and
node. Verify that its artifact digests match the retained files. The published
`hosted-task-receipt.json` records the following observations.

| Field | Source and expected value |
| --- | --- |
| `version`, `archive_sha256`, `run_id`, `task_id` | Version 1, the prepared archive hashes, the reserved run UUID and the Task UUID returned by MCP |
| `initial`, `recovered` | Each records `task_id`, `status: running`, `attempt` (1 and 2), `deployment_id` and `instance_id`. Both observations use the same worker deployment and distinct VM instances. |
| `stopped_instance_id` | The original worker instance confirmed stopped by the platform |
| `result` | The submitted `task_id`, `status: completed` and expected `text` from the MCP result |
| `observer` | Read-only PostgreSQL observation with `namespace: rehearsal-<run_id>`, the submitted `task_id`, `attempt: 2`, `task_rows: 1` and `completed_result_rows: 1`. Count all Task rows in the isolated namespace and completed rows with a stored result. |
| `cleanup` | `workloads_remaining: 0`, `database_present: false`, `logins_remaining: 0`, `secrets_remaining: 0`, and `sources_remaining: 0` from independent cleanup checks on the reserved resources |

```bash
cmp "$evidence_root/prepared/preparation.json" "$evidence_root/native/preparation.json"
python3 scripts/ci/rehearsal-native.py --verify-hosted-task "$evidence_root/native"
```

The verifier checks the consistency of these observations and the prepared
archive hashes and reports `receipt_verified: true` with `native_qualified: false`.
Use the enrolled run's passing qualification journal and leakcheck logs as the
native acceptance evidence. Keep the private evidence directory for each run.
If the run fails, retain its journal, logs and any pending receipt for
investigation. Once the owner approval is consumed, a repeat run requires a
fresh approval and a new evidence directory.

## Native host setup

Use a dedicated native x86_64 Linux KVM acceptance host with root, no customer
workloads, a Pro acceptance account, and a complete installation of the exact
clean checkout being qualified. The public API origin must route to that
installation. This is not a runner for a production fleet or nested KVM. It
holds `/var/lock/faas-builder-acceptance.lock` and refuses an initially unclean
leakcheck; it never stops or reaps another process to obtain a clean result.

Install root-owned regular files without group/other write permission:

- `/etc/faas/rehearsal-acceptance-host.json`, containing
  `{"dedicated_native_host":true,"api_origin":"https://ACCEPTANCE-API","account_id":"ACCEPTANCE-ACCOUNT-UUID"}`.
  This is the operator's attestation that the API, account and physical host form
  the same dedicated acceptance installation.
- `/etc/faas/rehearsal-acceptance-platform-sha`, containing the full installed
  platform Git commit. It must equal the clean checkout's `HEAD`, before and
  after the journey. Stage the matching daemons, kernel, guest resume hook and
  runtime assets before writing the stamp.

Configure the acceptance installation according to each adapter's ADR. Use a
separate disposable local PostgreSQL 16+ cluster with `gregale_rehearsal_admin`,
`max_prepared_transactions=0`, and no platform or customer databases. Set the
PostgreSQL admin URL only on apid and the matching loopback proxy target on the
private gateway. The object adapter uses its own rehearsal namespace and needs
no provider bucket or credentials. Do not substitute a control-plane database.

On this installation only, explicitly configure the existing containment,
simulation, command, PostgreSQL fixture, object fixture, managed-worker,
suspend/resume and running-job suspend gates. Containment also requires
`FAAS_REHEARSAL_NETWORK_CONTAINMENT=1` on the host's vmmd, not only on apid
([ADR-20261019120000](adr/20261019120000-rehearsal-runtime-hot-path-isolation.md)).
App tasks also require
`FAAS_APP_TASK_API_ENABLED=1` on apid and `FAAS_APP_TASK_DISPATCH=1` on schedd.
The acceptance runner changes none of these settings. Admission elsewhere
remains disabled until the relevant native matrices are complete and reviewed.

Supply `FAAS_API` and a dedicated account `FAAS_TOKEN` through the host's normal
credential mechanism. Never put credentials in a manifest, command argument or
evidence attachment. Also stage the existing metal pause prerequisite assets:
`FAAS_TEST_KERNEL`, `FAAS_TEST_BASE_ROOTFS`, `FAAS_TEST_LAYER_ROOTFS`,
`FAAS_TEST_FC_VERSION`, `FAAS_TEST_V6_BASE` and `FAAS_TEST_V6_LAYER`. Required
tools include the pinned Go toolchain, Git, make, Python 3, Firecracker, jailer,
busybox, ip, nft, mkfs.ext4, losetup and findmnt.

Run from the clean installed checkout, using a new evidence directory outside
the checkout whose parent already exists:

```bash
GREGALE_REHEARSAL_NATIVE_ACCEPTANCE=1 \
  bash scripts/ci/run-native-rehearsal-acceptance.sh \
  --out /var/tmp/rehearsal-native-UNIQUE
```

The runner builds the platform CLI and receipt verifier on the host. Application
builds and setup, trigger and observer commands use the ordinary builder and
app-task microVM owners. It first runs the real metal pause test, then executes
four isolated warm runs: two control runs and a baseline/candidate pair. Each
comparison has a 120 workload-minute admission budget; normal account quotas,
build deadlines and the ten-minute scenario deadline still apply.

`control.json` must pass. `regression.json` must fail only because the worker's
notification group has one versus two otherwise identical simulation attempts.
Exit code 1 without those complete receipts does not qualify. The verifier
recomputes comparison results and checks:

- The independently observed and sealed checkpoint identify the same original
  first dispatch, instance, deployment, recorder and simulated response.
- The one import completes after exactly two journaled attempts on that instance;
  all three contained commands have successful, intact retirement receipts.
- Independent collectors show the expected initial/final row, one CSV upload,
  matching contents, closed collection and confirmed database/object cleanup.
- Runtime and resource identities differ across the four runs. All eight
  isolated serving apps are independently absent through the API after cleanup.
- The native pause test actually passed, the installed source identity remained
  stable, and leakcheck was clean both before and after the journey.

Retain the private evidence directory, including failed runs. The journal starts
failed and becomes passed only after verification and final leakcheck. It binds
the artifacts by SHA-256 and records platform and fixture commits. On timeout or
interruption the coordinator signals only its own child process group, allows
the CLI up to three minutes for owner cleanup, and leaves the result failed.
Leakcheck observes resources before any manual recovery. A missing report,
cleanup error, remaining app or leaked VM requires investigation; never edit a
journal into a passing result or reuse an old output directory.

A passing journey covers this specific composition. Cross-run access denial,
real-packet firewall isolation, cancellation, collector loss/restart,
cleanup-failure recovery and expired-resource reaping still need the native
matrices required by the earlier rehearsal ADRs. Replacement-worker retries,
production snapshots and provider behavior remain
outside this journey. A compile or portable test result does not qualify native
operation or enable rollout.

For PostgreSQL recovery qualification, add `--journey postgres-recovery` to the
command above and use a separate new evidence directory. The first attempt must
commit and upload, await the checkpoint, observe a failed SQL query during the
fence, and retain its original process. The real managed retry must reconnect
with its fixture credentials and complete. The verifier requires independent
activation, at least one closed connection, recovery within five seconds, exact
enrollment/resource/worker identities, expected SQL and CSV, and notification
multiplicity of one versus two. A VM pause receipt cannot substitute for this
recovery proof. Native execution of this journey and its failure matrices remain
pending.
