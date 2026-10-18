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
suspend/resume and running-job suspend gates. App tasks also require
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
