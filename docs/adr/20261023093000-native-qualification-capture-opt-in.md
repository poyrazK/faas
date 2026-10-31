# ADR 20261023093000 · Native qualification capture opens behind a host opt-in

- **Status:** accepted for acceptance hosts; production stays closed
- **Date:** 2026-10-23
- **Related:** ADR-568 (environment GitOps; native capture/restore gate),
  ADR-1646 (candidate binding qualification), ADR 20261022184951 (native
  runtime capture), ADR 20261021153953 (push-bound worker listener),
  ADR 20261022155812 (pull-queue smoke leases)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

ADR-568 keeps native qualification capture closed until capture and restore
pass dedicated acceptance. In code that was
`JailerVMM.checkEnvironmentQualificationSnapshotSupport`, which refused every
capture unconditionally. The rest of the path, from the original candidate
through native capture, journal-backed restore, smoke and owner-authorized
retirement, is implemented. The gate also refused the acceptance run that is
meant to prove it, so the native GitOps and managed-service journeys could
never get past capture.

On the n2 test node, every earlier prerequisite now passes: nftables 1.1.3,
loose `rp_filter` on `br-tenants`, and the push-worker listener. A graph
reaches capture and stops there with
`environment_qualification_unconfirmed`. The native capture protocol itself
(`TestMetalNativeCaptureSequence`: complete, lost_create, lost_resume)
passed on n2 under the runtime-capture work.

## Decision

- **The opt-in.** vmmd gains `native_qualification_capture`, off by default.
  `LoadConfig` refuses it unless `native_process_recovery = true` and both
  `native_snapshot_publication_root` and `native_image_staging_root` are set.
  vmmd then calls `JailerVMM.WithEnvironmentQualificationCapture()`.
- **The gate.** It opens only when the opt-in and every native prerequisite
  are present on the jailer. A default or partially configured host keeps the
  unchanged refusal before any effect.
- **Acceptance hosts.** The e2e harness sets the option only for
  `GREGALE_GITOPS_NATIVE_ACCEPTANCE=1`, so the native GitOps journey is the
  acceptance for capture, restore, smoke and retirement on that host class.
- **Production.** No production template sets it. Enabling it on a production
  host needs a passing native GitOps acceptance on that host class and an
  operator decision. The decision must also settle the shared snapshot
  directory, because the exclusive publication writer refuses
  `/srv/fc/snap`'s group-writable 2770 mode, the same open question as
  ADR 20261022184951.

## Consequences

- Acceptance runs can reach restore, smoke and retirement natively, and their
  result is recorded below.
- The option's own refusal at load time prevents a half-configured host from
  booting candidates that every capture would then reject.
- Production behaviour is unchanged until an operator sets the option.

## Amendment 1 (2026-10-24): restore delivers its receipt control over the resume hook

The first n2 run (`bindq-e1837337bf`) booted both candidates, passed the
source smoke and native capture, and retired both VMs. The restore then
failed with:

```
native restore load: verify original layer.ext4: storage: artifact differs from original receipt
```

Restore was staging pre-boot files (secrets, API env with a fresh receipt
token, resolver, CA) into the captured `layer.ext4` before load. Load
verifies that layer byte-for-byte against the capture receipt, so every
restore failed. Writing into a filesystem the restored guest already has
mounted is unsafe anyway. Also, guest-init emitted its configuration receipt
only at startup. A restored guest never re-reads the env file, so even an
unchecked layer could not have produced the restored-target receipt that
ADR-568 requires.

- **No staging on restore.** Restore stages no pre-boot file and unseals no
  secret. The layer stays identical to the capture, and the strict load
  check is kept.
- **A fresh control on the resume hook.** vmmd generates a fresh one-time
  token and MAC key per restore attempt. It sends them in the resume-hook
  body as `qualificationReceipt`, an `omitempty` key, over the connection
  that is already checked against the pinned original process. The waiter
  expects the restore request's frozen projection: the API env digest and
  the MAC of the secret key names.
- **The guest re-attests.** A qualification boot that emitted its receipt
  keeps only the receipt basis: the API env digest and the sorted secret key
  names, never secret values. After it acknowledges the resume, guest-init
  re-emits a receipt with the same encoding under the fresh control.
  - It retries for up to 30 s, because vmmd opens the restore's guest-event
    channel only after the acknowledgement.
  - A guest without a boot basis withholds the receipt.
  - A malformed control is ignored.
  - Both cases fail closed on the host.
- **The host waits while the channels are open.** The restore's private
  guest channels close when the native restore returns. A restored app is
  ready almost at once, so vmmd consumes the receipt inside the restore,
  after readiness and before the channels close. The first n2 run with this
  amendment (`bindq-0d1f6f3315`) showed the receipt leaving the guest
  113 ms after resume. vmmd then cancelled the stream when the channels
  closed, because it only waited after the restore had returned.
- **The expectation uses the capture's instance identity.** A snapshot
  restore cannot change the environment of a running process, so the
  restored guest keeps the `FAAS_INSTANCE_ID` it booted with. That is the
  capture instance's ID, while schedd builds the restore spec for the target
  instance. `FAAS_INSTANCE_ID` is the only per-instance entry: the restore runs
  under the same request and graph, on the same node, so binding URLs match.
  - vmmd expects the restore request's env with that entry set to the
    capture instance.
  - schedd records the same digest for the restored target
    (`fcvm.QualificationRestoreAPIEnvSHA256`).
  - The n2 rerun with the in-restore wait had the receipt rejected
    (`did not match this attempt`) until this change.
- **The restored smoke reaches its target.** schedd probes a restored graph
  through vmmd's private HTTP forward, which resolves instances with
  `NetnsFor`.
  - A source candidate boots through the generic wake, so it sits in the
    serving index, flagged `QualificationOnly`.
  - A restored target stays in the private qualification registry, so the
    probe failed with `instance "…" not live`.
  - `NetnsFor` now also resolves a restored qualification target (restore
    method, not a Job) from that registry.
  - On a native host the forward also needs the captured public forward
    target. The restore now enables public forwarding on its network, as the
    wake does, and captures the target after network setup.
    `PublicForwardTarget` resolves it under the same rule.
  - HTTP forward admission (ADR-570) admits instances only from the serving
    index. It now resolves restored targets under the same rule. Their
    admission generation retires through the shared cleanup.
  - Together these give the probe the same reach a source candidate already
    has. The serving index and routing are unchanged.
- **Framework-ready evidence (decided 2026-10-26).** Only a function runtime
  runner signals framework-ready. It records the signal in guest memory,
  which a snapshot keeps, and serves it when the host reads guest port 1027.
  It never pushes the signal again after a restore.
  - A candidate whose app has no runtime (`apps.runtime` empty) never
    signals framework-ready, and production stamps none for it. Its graph
    member now carries `no_framework_ready_signal`, and activation evidence
    no longer requires that receipt for it.
  - Graphs stored before this change keep the requirement: the field is
    absent there, which fails closed.
  - For a runner candidate the restore reads the stored state from the
    restored guest after the native restore succeeds. vmmd records the
    framework-ready receipt for the restore frame before it publishes the
    target, using the frozen runtime label.
  - The n2 run with 4919977311 completed the whole qualification dispatch
    for the first time. Activation then waited only on this receipt for two
    full-rootfs image candidates.
- **What the receipt proves.** A capture made from different inputs cannot
  satisfy the restore request's expectation. Restore still refuses sidecars,
  so only the main workload re-attests.
- **Version skew.** An older guest ignores the new key and sends no receipt,
  so the restore fails closed instead of qualifying without proof.

## Acceptance record

- 2026-10-23, n2 `bindq-e1837337bf`: capture passed; restore failed at the
  layer verification described in amendment 1.
- 2026-10-24/25, n2 `bindq-0d1f6f3315`:
  - The Linux unit tests passed.
  - The restore loaded the capture with the strict layer check, and the
    resume hook was acknowledged.
  - The receipt was dropped as described above.
- 2026-10-25, n2, rerun with b5819ddd5a on a private PostgreSQL (the shared
  e2e cluster was down after a full root disk):
  - The gitlab restore received the guest's receipt inside the restore.
  - vmmd rejected the receipt because of the instance identity mismatch
    described above.
  - The github capture failed on a full scratch disk.
- 2026-10-26, n2, rerun with 7f95c9cbee on a private PostgreSQL:
  - All four graph members, two per provider, were captured and restored.
  - Every restored guest's receipt arrived within 110–292 ms and was
    accepted.
  - The restored-graph smoke then failed with `not live`, as described above.
- 2026-10-26, n2, rerun with 170e8ed217 (`NetnsFor` only):
  - Restore and receipts passed again.
  - The probe then failed with `public forward instance is not live`.
- 2026-10-26, n2, rerun with 4919977311:
  - The github graph completed the whole native qualification dispatch:
    capture, restore, guest receipt, restored-graph smoke and retirement.
  - Activation evidence then lacked only framework-ready (both candidates
    are image apps), activation and serving.
- 2026-10-27, n2, the branch merged with main (1da4b81a7e):
  - vmmd refused the shared native journal. Another session's older vmmd had
    left launch records without the newer lease fields.
  - With the user's approval, the journal was moved aside, not deleted.
  - The rerun passed restore and receipts. The restored-graph smoke then
    failed on HTTP forward admission, as described above.
- 2026-10-28, n2, rerun with 46e36b254e:
  - The gitlab graph completed the whole native qualification dispatch on
    the merged tree.
  - The github candidates failed to deploy before qualification. imaged
    staged the shared minimal base for two plain-image deployments at once.
    The first one's scan publication then failed as stale (`state: runtime
    inputs changed after admission`).
  - ac56849e87 serializes minimal-base staging, as runtime bases already
    were.
- 2026-10-28, n2, rerun with ac56849e87:
  - Every candidate in both graphs finished preparation.
  - The gitlab graph qualified natively for the first time: two captures,
    two restores, two guest config acknowledgements and two smokes. Its
    evidence reported `Qualified`. Activation had not happened when the
    test's 12-minute window closed, about 4.5 minutes after qualification.
  - The github capture's cold boot spent 33.8 s provisioning the jail chroot
    on the busy shared disk. It overran schedd's cold-boot budget, and the
    5-minute qualification lease held the retry past the test window.
  - 939289c79e makes the test report the GitOps runs and the graph,
    qualification and release rows when it times out.
- 2026-10-29, n2, rerun with the timeout diagnostics:
  - gitlab qualified again. Every apid pass then ended
    `environment_runtime_unacknowledged`, and no release set existed.
    - The test fixture's apps had no revision pin TTL. Activation publishes a
      project release set, which refuses such an app, so it returned "not
      activated" without an error.
    - a52d0adfb6 sets the TTL, as the source-Jobs journey already did.
  - The github capture failed with `create original snapshot: unexpected
    EOF`.
    - vmmd's cgroup fence repair had written the ordinary `memory.max` back
      into the capture's scope, and the kernel OOM-killed Firecracker while it
      wrote the snapshot.
    - The native snapshot allowance (ADR-568) raised the limit without moving
      the fence record, unlike `widenSnapshotFence` on the ordinary path. This
      affects every native capture, runtime capture included.
    - 66cf2736ae records each limit in the fence before writing it.
- 2026-10-29, n2, rerun with 66cf2736ae and a52d0adfb6:
  - Both graphs, github and gitlab, were captured, restored, qualified and
    activated natively for the first time.
  - The first customer queue delivery then failed a test check. A bound
    queue keeps its environment on the binding, not on the invocation.
    3366c8253d checks the binding instead.
- 2026-10-29, n2, rerun with 3366c8253d:
  - The first queue delivery dead-lettered. The test created its apps with
    no node, and queue delivery routes through the app's node. 8072f211e5
    places them on the harness node.
  - The worker's first serving cold boot timed out on the loaded host. schedd
    could not mark it failed:
    - The legacy-qualification fallback of the ADR-435 retirement guard
      covered every frameless instance of a deployment with an
      `environment_workload_runtime`.
    - Activation keeps that runtime, so the ordinary serving instances of an
      activated GitOps workload could never fail, park or be removed.
    - 09b75d2ce7 limits the fallback to held candidates, in a new migration
      with a Postgres test.
  - The rerun with those fixes routed the delivery. The worker's serving
    cold boot then succeeded, but recording its runtime receipt was refused:
    the runtime-receipt guard also treated every instance of such a
    deployment as a qualification attempt.
    - 680403868b lets a frameless instance of an unheld live deployment
      through, as the instance reservation guard already did.
    - It is in the same unreleased migration and is covered by the same
      Postgres test.
- 2026-10-29, n2, rerun with 680403868b, after the user approved removing
  two leaked builder jails another session left behind (their TUN device
  mounts stopped every native vmmd from starting):
  - github: the worker's serving wake succeeded and the first customer
    delivery reached `dispatching`. Finalizing it then failed with `function
    public.environment_workload_serving_ready(uuid) does not exist`.
    - Two serving queries schema-qualified that function, unlike every
      other schema function. That only breaks per-test schemas.
    - 59adf9a06d calls it unqualified.
  - gitlab: the second member's restore ran out of time while it staged its
    snapshot inputs on the loaded host. The partial copy then failed its
    receipt check. Earlier runs restored the same graph.
- 2026-10-29, n2, rerun with 59adf9a06d:
  - The gitlab graph was activated natively, its release set went live, and
    the first customer queue delivery completed.
  - Serving convergence then failed on every apid pass
    (`environment_runtime_verification_failed`) until the 4-minute serving
    window closed.
  - github's capture was cancelled while copying its private image on the
    loaded host.
- 2026-10-29, n2, rerun with an uncommitted diagnostic that logs the
  verification error:
  - Both graphs, github and gitlab, were activated and completed their first
    customer queue delivery.
  - Every later apid pass then refused the activated candidate: `stored
    candidate inputs differ from the reviewed intent`.
    - The frozen inputs carry an inherited baseline, the app's live
      deployments at review. On Postgres that baseline includes the
      candidate itself once activation makes it live.
    - c23c5fd766 carries the frozen baseline over for an activated (live,
      unheld) candidate. Every other authority field must still match.
- 2026-10-30, n2, rerun with c23c5fd766:
  - The candidate refusal is gone, and both graphs were activated again.
  - Serving still failed on every pass, now with a bare `state: conflict`.
    Many store calls return that.
  - 44e992a7f0 names the failing step in the verification error.
- 2026-10-30, n2, rerun with 44e992a7f0:
  - The named step was `reconcile preparation`. Preparation reconciliation
    compared the activated candidate's inputs strictly, and so did source
    requests.
  - eb173a4d6d routes all three post-activation checks through one helper
    with the activated-candidate comparison. Activation itself keeps the
    strict match on a held candidate.
- 2026-10-30, n2, reruns with eb173a4d6d and then f4121638b6. Each cleared
  the next refusal that came after activation:
  - The preparation reconcile computed `preparing` for live candidates and
    tried `prepared` → `preparing`, which the graph guard refuses.
    f4121638b6 counts a live candidate as prepared.
  - Qualification was re-queued on every pass. Its guard admits only
    snapshotting candidates. f86f27291e queues nothing for an activated
    graph.
  - gitlab's vmmd once stalled in native recovery right after github's
    teardown and never opened its socket. Every earlier run started.
- 2026-10-30, n2, rerun with f86f27291e:
  - The github graph made it through every apid pass without an error.
    Serving convergence still never completed: the journey ran no serving
    gateway, so there was no route acknowledgement to wait for.
  - 17923ec133 registers gatewayd as the default-local serving gateway, as
    the edge-rule and runtime-policy journeys do.
  - gitlab's qualification cold boot timed out on the loaded host.
- 2026-10-31, n2, rerun with 17923ec133:
  - The first attempt never started. vmmd refused the shared native journal,
    whose launch records another session's newer vmmd had written.
  - After that session moved the journal aside, the rerun activated github.
    Serving preparation then failed with `function
    public.environment_workload_scheduled_job_acks(uuid, uuid) does not
    exist`, the same qualification bug as 59adf9a06d.
    - 4472b400d9 calls it unqualified. No other faas query qualifies a
      schema function.
  - The first queue delivery dead-lettered. All five attempts ran out while
    the worker's cold boots waited on slow `nft` and `debugfs` steps.
  - gitlab's capture was cancelled mid-copy of its private image.
- 2026-10-31, n2, rerun with 4472b400d9:
  - Both graphs, github and gitlab, were captured, restored, qualified and
    activated natively, and each completed its first customer queue
    delivery.
  - github's serving evidence completed (`Serving:true`, no blocking
    reasons).
  - The applied revision was still never recorded. Every apid pass ended
    `environment_runtime_unacknowledged`, which points at the runtime
    readiness checks that follow serving convergence.
- 2026-10-31, n2, rerun with an uncommitted readiness diagnostic:
  - The blocker was the runtime target's `UnqualifiedWorkloads:1`.
  - That fence comes from the ADR-568 preview. It counts every Git-managed
    workload source as unqualified, and nothing clears it, so an enforced
    graph could never record its applied revision.
  - 6bc1801865 treats the fence as met when the reviewed graph qualified,
    activated and served in the same pass. Freshness checks still apply,
    and report mode keeps the fence.
- Pending: the n2 native GitOps journey through serving and the resilience
  phases.

## Rejected alternatives

- **Remove the gate.** That would open capture on every native host before
  any acceptance.
- **Tie it to `native_runtime_capture`.** Runtime capture and qualification
  capture have different authority and retirement paths, and each needs its
  own acceptance.
