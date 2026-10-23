# vmmd adopts running VMs across restarts

Date: 2026-10-17
Status: Proposed (design only; nothing below is built)
Related: spec §4.4, §6, §6.2, §11, §12, §14; ADR-005, ADR-009, ADR-191, ADR-205,
ADR-435/595 (runtime-admission incarnation), ADR-469, ADR-471, ADR-472–479,
ADR-631, ADR-971, ADR-1151, ADR-1344 §4 and §6; ADR-1027's database-VM
adoption (commit c0458f6400 on local branch `feat/native-managed-postgres`);
`20261017-connection-survival-across-data-path-restarts.md` (the
connection-survival ADR; both are built together on `feat/vmmd-no-drain`)

## Summary

Today, installing a release drains the whole compute node because the
release restarts vmmd, and a restarted vmmd cannot use the VMs it did not start.
This ADR makes a vmmd restart non-destructive. The new vmmd adopts every
instance whose journal record proves ownership. It rebuilds that instance's
in-memory state, reconnects to its Firecracker process and guest channels, and
reports the adopted set to schedd under a new ownership epoch. Anything it
cannot prove keeps today's ADR-472 quarantine. After this, a release drains a
compute node only for a host or kernel reboot. This is the detailed design behind
ADR-1344 §6's "Non-destructive vmmd restart" bullet.

## Context

### What a vmmd restart does today

- Firecracker survives. Jailer moves each VM into
  `faas.slice/faas-tenant.slice/faas-tenant-<plan>.slice`, a sibling of
  `faas-cp.slice` where vmmd runs. So the unit's default `KillMode=control-group`
  does not reach it (`cmd/vmmd/main.go:1179`). Jailer runs without
  `--daemonize` or `--new-pid-ns` (`pkg/fcvm/config.go` `JailerCommand`), so the
  Firecracker PID is vmmd's direct child.
- vmmd's state is in memory and dies with it:
  - `Manager`: `live`, `alloc`, `cidToID`, `processGenerations`,
    `appEgressPolicies`, `appCPUPolicies`, `runtimeAdmission*`,
    `pendingCleanup`, `readinessLoopCancels`, `cooldownByDeployment`,
    `appResolved` and others.
  - `JailerVMM`: `proc`, `recs`, `clients`, `guestVsockListeners`, `rings`,
    `cpuBoostTails`, `runtimeDriveHandoffs`, `materialisedTmp`,
    `materialisedIdentity`, `bindMounts`, `bindSourceModes`, `ownedJails`,
    `fences` and others.
- The new vmmd recovers resources but not ownership:
  - It quarantines what the resource journal and `/proc` show
    (`recoverRestartResources`, `cmd/vmmd/main.go:1037`).
  - `ReapOrphanedJails` kills survivors whose instance rows are no longer live
    (`cmd/vmmd/main.go:1204`).
  - Survivors that the database still considers live keep running, but nothing
    can reach them. Wakes, stops and forwards to them fail. ADR-191's divergence
    sweep reports their rows, and fails them only when
    `FAAS_SCHEDD_RECONCILE_ENFORCE=1` is set.
- Each live VM loses its link to vmmd in three places:
  - Its stdout is a pipe into vmmd (`startJailer`, `pkg/fcvm/vmm.go:5290`).
  - Its exit watch is a `cmd.Wait` goroutine.
  - Its guest-initiated vsock ports (framework-ready, job exit, runtime config,
    workload identity, outbound relay, profiles, traces) are listeners that
    vmmd holds.
- vmmd's per-instance stream bridges are started with `ip netns exec` inside
  vmmd's cgroup (`pkg/vmmdgrpc/forward.go:1519`), so they die with the unit.
- vmmd runs with `ProtectSystem=strict`, so systemd gives every start a fresh
  mount namespace. Mounts that the previous process made in its own namespace
  are not visible to its successor. That includes image binds into jails and,
  depending on propagation, the `/run/netns/<name>` nsfs bindings
  (validation V-A1 below).
- So every release drains first (`deploy/ansible/node_join.yml:1138`). The drain
  migrates each instance: a capture on the source node plus a restore on the
  destination. node_join allows up to 10 minutes per node. Then `node_join.yml:1211` stops vmmd
  and `node_join.yml:1535` restarts it.

### Production baselines (production-us, 2026-10-17, read-only)

- **VMs per node.** Over the last 7 days, peak concurrent live VMs were 8 on
  fsn-2 and 14 on fsn-3. Instance starts in that period were 3,855 and 19,143.
  The RAM ceiling admits far more.
- **Rollout time.** In the last two successful rollouts (cd-platform runs
  38985388820 and 38726786194), "Join or roll compute node" took 22–28 min per
  node:
  - runtime-base pre-stage: 5.0 min;
  - ~720 of 752 ansible tasks, each under 5 s: about 12 min in total;
  - the drain tasks: only 1.5 min, because each node had about 1 live VM.
- **Drain cost at real load.** Each migration costs about 15 s of capture per
  256 MB guest, plus a restore. At 14 VMs the drain, not the rest of the play,
  becomes the largest part of the node's time. Removing it is what this ADR
  buys. The other ~17 min belong to the per-unit release work.
- **Wake hold.** The gateway holds requests for 30 s (queue cap 512). Adoption
  must finish well under that.

### What ADR-472–479 already provide

ADR-472–479 run in the **default** mode (`native_process_recovery = false`).
ADR-971's production-us record counts come from this mode's journal in
`/var/lib/faas/vmmd-resources`. ADR-631 says production runs
`native_process_recovery`, which contradicts that; V-A0 checks the flag on every
compute node before slice 2. In the default mode the persistent resource journal
records:

- lease intent;
- process incarnation: boot ID, PID, start ticks, argv and UIDs;
- staged files and image binds;
- jail directories and named netns bindings;
- host veth ifindex and MAC;
- prepared spares.

ADR-971 retires dead records whose resources are verifiably gone. All of these
ADRs say the same thing: **records are provenance, never reconstructed
ownership.** None of them rebuilds the live map, watchdogs, mounts, cleanup
authority or the scheduler's view.

Two points are easy to misread:

1. `native_process_recovery` is a different, stricter lifecycle. Its launch
   journal lives in `/srv/fc/jail/.native-processes` and it is experimental and
   off in production. ADR-472–479 are not that mode. This ADR builds on the
   default-mode resource journal, the one that runs in production. Native mode
   keeps its strict quarantine and keeps draining (see "Boundaries").
2. Resource-journal record versions 1–6 already exist: assets 2, placement 3,
   veth 4, prepared spares 5 and 6 (`pkg/fcvm/resource_assets.go`,
   `pkg/fcvm/resource_prepared.go`). The next step after ADR-473–475 is
   therefore **record version 7**, called "the adoption record".

### Prior art in this repository

ADR-1027's database-VM runtime already adopts its VMs across vmmd restarts. That
code is on a local branch, not on main. It uses:

- a per-jail record outside the chroot;
- `fcvm.Allocator.Adopt(slot)`;
- a pidfd exit watch;
- a console FIFO in place of the per-process pipe;
- re-listened relays.

On metal it verified that Firecracker survives SIGPIPE with no console reader.
This ADR applies the same pattern to app VMs, adds the provenance checks that
ADR-472–479 require, and adds scheduler reconciliation.

## Decision

### The adoption proof

The new vmmd adopts an instance only if **all** of the following hold. Otherwise
the instance stays in ADR-472 quarantine, exactly as today, and is reported as
`refused` with a reason.

1. **Record.** A version-7 record with a committed adoption block. The record has
   no unknown fields and its kind is in the adoptable set ("Workload kinds").
2. **Same boot.** The record's kernel boot ID equals the current boot ID.
3. **Same process.**
   - The PID is alive and its start ticks match the record.
   - All four kernel UID fields equal the lease UID.
   - The `--id` argument equals `PerInstanceScope(instance)`.
   - `/proc/<pid>/exe` has the recorded device and inode. This works after the
     release replaced the binary on disk.
   - vmmd opens a pidfd, then re-reads the start ticks through it. This closes
     the PID-reuse window between the scan and the open.
4. **Same cgroup.**
   - `/proc/<pid>/cgroup` is the expected per-plan scope.
   - `memory.max` is plan + 8 MB, or the ADR-205 value recorded for the
     instance.
   - A drifted fence is repaired first (the `runCgroupFenceRepair` logic), then
     the instance is adopted.
5. **Same network** (networked kinds only).
   - `/proc/<pid>/ns/net` has the recorded netns inode.
   - The recorded host veth (ifindex and MAC) exists, and its peer is in that
     namespace.
6. **Firecracker answers.** On the jail's API socket, `GET /` returns the expected
   instance ID within 250 ms, with state `Running` or `Paused`.
   `GET /machine-config` matches the recorded vCPU count and memory.
7. **Ownership epoch.** This vmmd claimed the node's next ownership epoch from the
   epoch recorded in the journal (§3). The claim happens once per start, before
   any adoption effect.

When the proof holds, the new vmmd gets the old one's full cleanup authority over
that record's assets. This replaces ADR-472–475's "never reconstruct ownership
maps from journal records" for adopted records only. Quarantined records keep
every existing restriction.

### 1. Journal record version 7: the adoption record

The record keeps the ADR-473 storage rules:

- private 0700 directory;
- exclusive flock;
- temporary file, fsync, atomic rename and directory fsync;
- strict field set;
- 256 KiB bound.

It adds one `adoption` block. Every value in it is non-secret. Like ADR-473, it
never stores environment, sealed or plain secrets, commands, credentials or
artifact contents.

| Group | Fields |
|---|---|
| Ownership | `epoch` and `incarnation` of the vmmd that last wrote the block |
| Kind | `app` (service, function or worker mode), `job`, `app_task`, `execution`, `builder` |
| Firecracker | exe path, device and inode, Firecracker version, launch method (cold boot or restore plus snapshot ID), vCPU count, memory MiB, API socket path |
| Guest | drive0 base digest, guest-init protocol version, drive1 identity |
| Instance | app, account and deployment IDs; plan, port, healthcheck path, execution mode, runtime, workload names, liveness and readiness probe JSON, startup deadline, quiet-pause-after, image-healthcheck flag; boot, ready and framework-ready times; startup CPU boost deadline |
| Network | nft handles (`AllowlistHandleV4/V6`, `PrivateNetworkHandleV4/V6`), applied egress policy revision and hash, applied CPU policy revision, static egress IP, DNS-gating state |
| Runtime admission | receipt token, binding payload hash, issuing incarnation, captured input hash, egress revision |
| Guest channels | vsock ports bound for this instance; console file path |
| Lifecycle | `state` (`ready`, `frozen` or `stopping`), frozen-since, and at most one `op` intent: kind, token, phase, start time, and the paths of artifacts it is creating |
| Bridge | One entry per bridge generation (connection-survival ADR §2, §5): transient unit name and InvocationID, bridge uid, main PID and start ticks, exe device, inode and digest, protocol version, registry generation directory, gate state. Empty until bridges run as transient units (connection-survival P1c) |

**When vmmd writes it:**

- **Readiness.** The block is committed once, right after a Wake, restore or job
  boot reaches readiness. The commit runs **off the wake critical path**: Wake
  returns first, then the commit follows under the per-instance journal lock.
  This is safe because a crash before the commit leaves the record without a
  block, so the instance is quarantined as today. The ~1–3 ms fsync therefore
  never touches the 350 ms restore budget.
- **Before an effect.** An `op` intent is written before the effect starts and
  cleared when it completes. This applies to snapshot, warm snapshot, park,
  freeze, thaw, stop, migrate-out and migrate-in. Each of these already costs
  far more than one fsync.
- **After a change.** The block is rewritten after a policy handle, applied
  revision or `frozen` state changes, before the RPC acknowledges.
- **Never for quiet pause.** Quiet pause (ADR-1151) flips between requests, so
  it is not journaled; adoption reads the pause state from Firecracker instead.

A separate small per-instance file, `console.offset`, holds the console-read
offset. It is written without fsync every 5 s or 1 MiB. Its loss only replays
console lines that offset-based dedupe drops (§2).

**Version rules.**

- Older binaries reject version 7 by design (ADR-473). So the reader ships one
  release before the writer: release R accepts version 7 and treats it like
  version 6, and release R+1 writes it.
- Later block changes follow the same expand/contract rule. A release reads
  version N+1 one release before it writes it. A rollback to an
  adoption-capable release can therefore always adopt. A rollback to a
  pre-adoption release quarantines, as today.

### 2. Adoption on start

vmmd's startup order becomes:

1. open the journal;
2. claim the epoch;
3. adopt;
4. reconcile prepared networks;
5. serve RPCs.

Work that adoption and admission do not need moves after serving:

- the log-archive spool walk (137 s on fsn-3 once, H10-80);
- failure-report spool scans;
- `PrimeBackingDigests`;
- the layer-clone sweep.

These start in the background or are gated per RPC. The adoption pass runs one
goroutine per instance, bounded by the slot count. For each adopted instance it
does the following.

- **Maps.** Rebuild the instance's entries:
  - `live` and `cidToID`;
  - its slot (an `Allocator.Adopt(slot, instance)` taken from ADR-1027, which
    removes the slot from the free stack and the quarantine set);
  - `processGenerations`, starting a new local generation;
  - the instance's `Instance` fields from the block;
  - `JailerVMM` `recs` and `proc`, using a pidfd-backed process handle in place
    of an `exec.Cmd`;
  - `materialisedTmp`, `materialisedIdentity`, `runtimeDriveHandoffs` and
    `ownedJails`, rebuilt from the record's assets after their identities are
    re-checked;
  - `bindSourceModes` refcounts, rebuilt from **all** adopted records that bind
    each source inode. A quarantined record that binds the same inode still
    blocks mode restoration, as in ADR-474.

  A test classifies every map field of `Manager` and `JailerVMM` by reflection as
  rebuilt, reset, re-pushed or boot-only. It fails when someone adds a field
  without classifying it, so new in-memory state cannot silently escape
  adoption.
- **Firecracker API.** Build a fresh `http.Client` on the jail's API socket. The
  socket is on the host tmpfs `/srv/fc/jail`, which every mount namespace sees.
  Verify as in proof step 6. If the state is `Paused` and the record is not
  `frozen`, the pause was a quiet pause: resume it. The new quiet-pause loop
  pauses it again after one full idle period.
- **Guest channels.**
  - vmmd stores its listening sockets in systemd's file descriptor store
    (`FDSTORE=1`, `FDNAME=gvsock-<instance>-<port>`). It gets them back
    through `LISTEN_FDS` and `LISTEN_FDNAMES`. As built (2026-10-18): systemd
    rejects `:` in a name, which separates `LISTEN_FDNAMES`; each job-exit
    listener is stored when it is bound and removed when its receiver is
    done, not at the stop, so a crash restart finds it too; and the gRPC
    listener is not stored, because the restart notice's retries cover it.
    Registered receivers re-bind (below).
  - systemd should also keep the store across crash restarts
    (`Restart=on-failure`). V-A2 confirms this on the installed version.
  - Guest connects during the gap queue in the backlog instead of failing.
  - Without a stored descriptor, vmmd unlinks and re-binds the same
    `v.sock_<port>` path. Firecracker connects by path, so the next connect
    reaches the new listener.
  - Unmatched stored descriptors are closed.
  - The unit gains `FileDescriptorStoreMax=4096`.
- **Console.**
  - Firecracker's stdout and stderr become the console file itself
    (`/var/log/faas/vm-<id>.console`, `O_WRONLY|O_APPEND`). They are no longer a
    pipe into vmmd.
  - vmmd tails the file into the ring (inotify, then read), and the ring keeps
    each line's file offset.
  - The adopting vmmd seeks to `console.offset` and catches up. Lines written
    during the gap are kept, and duplicates are dropped by offset.
  - This also removes today's back-pressure, where a stalled copy goroutine can
    block Firecracker's serial writes.
  - Retention is unchanged: the file lives for the instance and was already
    written in full.
  - The builder halt watcher already reads this file.
- **Exit watch.**
  - The adopting vmmd is not Firecracker's parent. Firecracker was re-parented
    to PID 1, which reaps it.
  - vmmd polls the pidfd. On exit it calls the same `processExitAttemptSink`
    with exit code `-1` (unknown).
  - Guest-reported causes (job exit, OOM, liveness) stay authoritative.
  - Kills use `pidfd_send_signal`, never a PID.
- **Network namespace.**
  - vmmd checks that `/run/netns/<name>` in its own mount namespace is an nsfs
    mount of the recorded inode. If not, it bind-mounts `/proc/<pid>/ns/net`
    there.
  - It then writes a new placement checkpoint under the current boot and mount
    namespace.
  - Every later `ip netns exec` needs this binding: nft updates, conntrack,
    and today's stream bridge start.
  - The netns is proven present through the pidfd-verified process, so this
    makes no absence claim. ADR-475's same-creator-context rule for
    **absence** still holds.
- **Mounts.**
  - Image binds that the previous vmmd made exist only in its old mount
    namespace and in Firecracker's own jailer-unshared copy (V-A1 confirms
    this). Both go away with their last process.
  - When an adopted instance is torn down, vmmd first confirms Firecracker is
    gone and no process in `/proc/*/ns/mnt` holds the recorded creator mount
    namespace. Then it retires the bind assets.
  - It restores source modes through the rebuilt refcounts.
  - Jail tree removal keeps ADR-475's "no remaining host-visible mount" check.
- **Loops.**
  - Restart liveness and readiness loops from the recorded probe JSON. The
    liveness failure count starts fresh, with one probe period of grace.
  - Re-arm the startup CPU-boost tail from its recorded deadline, or apply the
    steady `cpu.max` at once if the deadline has passed.
  - Stats read the cgroup paths derived from the lease.
  - Quiet pause and freeze skip an adopted instance until one idle period
    passes, because the activity tracker restarted at zero.
- **Bridges.**
  - Until the connection-survival ADR lands, remove the stale
    `/var/run/faas/stream/<instance>.sock`. vmmd starts the bridge again on the
    next forward, as it does today, through the re-anchored netns.
  - After that ADR (P1c), vmmd verifies each recorded bridge generation by
    the connection-survival ADR's proof: unit, InvocationID, uid, cgroup and
    netns inode match the record, `/proc/<pid>/exe` matches the recorded
    identity, and the control socket answers a hello naming the same
    instance and incarnation. vmmd then reconnects its control connection.
    The registry under `/run/faas-ibridge/` is tmpfs and survives the
    restart, so forwardd keeps routing to the same `serving` generation and
    nothing is re-published.
  - A bridge whose process is verified absent is replaced by a new
    generation before the instance is reported adopted. Its streams are
    lost, but the guest keeps running.
  - A bridge that is alive but fails the proof is quarantined and never
    killed blind. Its uid stays reserved, so the instance cannot get a new
    bridge: the instance is refused with reason `bridge_unproven`.
- **Interrupted teardown.** A record whose lifecycle says `stopping` had a failed
  or interrupted teardown (ADR-631's retained cleanup). Under the proof (or a
  verified-absent process plus verified assets), the new vmmd resumes that
  teardown instead of quarantining the slot. This removes the class of slot
  leak that ADR-971 describes for records whose resources survive.
- **Prepared spares.**
  - Spares are not adopted. A planned stop already closes them
    (`ClosePreparedNetworks`).
  - After a crash, unclaimed version-6 spares from the same boot and the
    predecessor epoch are torn down once their recorded identities verify.
  - Otherwise they stay quarantined as in ADR-477.

Budget. The targets must hold at 14 VMs (production's 7-day peak per node) and
at 48 VMs (about 3.5× that peak), or at fsn-4's admission ceiling if that is
lower:

| Measure | p99 target | Hard fail |
|---|---|---|
| Adoption pass | ≤ 2 s | — |
| Restart gap: predecessor stops accepting → successor serves with every instance adopted | ≤ 5 s | > 15 s (half of the gateway's 30 s wake hold) |

Slice 5 reports both measures at 1, 14, 32 and 48 VMs. If the cost grows with
VM count, the curve shows where the 5 s target would break.

The heartbeat's 3 × 30 s failure window is never approached.

vmmd's stop is bounded too. On SIGTERM it stops accepting and stores its
listeners. Long-polling and streaming RPCs (`WaitJobExit`, `Logs`,
`ForwardHTTPStream`, `ExecuteExecutionStream`, `AttachAppTask`) get
`UNAVAILABLE` with a "vmmd restarting" detail after a 2 s grace. vmmd then exits
without waiting the unit's 90 s stop timeout. In-flight lifecycle RPCs leave
their `op` intents for the successor.

**Rejected for now: in-place re-exec.** The old process could `execve` the new
binary with the same PID. It would stay Firecracker's parent and keep its
pipes, listeners and mount namespace. That covers only planned restarts with
an unchanged unit and environment, so it does not remove the need for cold
adoption. It stays a possible later optimization if slice 5 shows that exit
codes or the gap matter.

### 3. Scheduler reconciliation and mid-flight operations

**Ownership epoch.**

- A new column holds it: `compute_nodes.vmmd_epoch bigint NOT NULL DEFAULT 0`,
  with a CHECK `>= 0`.
- The journal directory gains `epoch.json` holding `{node_id, epoch, boot_id,
  state}`.
- On start, vmmd:
  1. writes `{epoch: E+1, state: claiming}`;
  2. runs `UPDATE compute_nodes SET vmmd_epoch = E+1, vmmd_incarnation = $new,
     vmmd_predecessor_incarnation = vmmd_incarnation, … WHERE id = $node AND
     vmmd_epoch = $E`. This extends `RegisterComputeNodeRuntimeIdentity`.
  3. writes `{E+1, claimed}`.
- A claim that completed in the database but not in the file is recognized and
  continued.
- A database epoch that no claim of this node explains means someone else
  advanced the node, for example an operator re-registration or a later
  ADR-1344 takeover. vmmd then adopts nothing, quarantines everything as
  today, and raises `FaasVmmdAdoptionFenced`.
- The epoch orders vmmd's inventory claims. Schedd ignores adoption reports
  and capacity telemetry that carry an epoch below the row's. Failure reports
  (ADR-471) state facts about one guest, not an inventory, so a report spooled
  by the predecessor and delivered by the successor still applies under
  ADR-471's existing ownership checks.
- When ADR-1344 §4 node leases exist, adoption also requires that this boot
  still holds `node/<name>`. A vmmd restart is not a change of holder, so the
  lease epoch does not move.

**Runtime-admission incarnation: admitted guests are not adopted.**

The incarnation stays a random per-process value (`Manager.RuntimeAdmissionIdentity`).
Replay protection for boot grants lives in process memory
(`runtimeAdmissionTokens`). Every grant names its incarnation, so a successor
cannot be replayed into consuming its predecessor's grants.

The original design re-bound an adopted instance's admission receipt: schedd
would update the stored binding to the new incarnation in a write fenced by
the new epoch. Building it showed that this cannot work as written (2026-10-18):

- ADR-595's `application_standard_native_boot_guard` and
  `application_standard_native_promotion_guard` triggers make the boot and
  promotion history immutable: `binding` never changes, and `receipt` never
  changes once set.
- `application_standard_lock_snapshot_capture` requires the stored receipt's
  binding to equal the saved grant's and its incarnation to equal
  `compute_nodes.vmmd_incarnation`, and vmmd checks a capture grant's parent
  against the same current incarnation.
- Live migration has no re-bind either: an admitted runtime gets fresh boot
  records instead.

So a re-bind needs an ADR-595 extension (an append-only re-bind record that
the lock functions resolve through), not a write in this ADR. That extension
is designed in `20261018-runtime-admission-rebind.md` (proposed). Owner decision
(2026-10-18): until such an extension exists, a guest booted under an
admission receipt is refused at adoption (`admission_receipt`). Its row fails
through the report and the app is re-admitted fresh. Apps under application
standards therefore see one re-admission per vmmd restart; ADR-595 is
unreleased, so no production app is affected today. The ADR-595 check in
`pkg/sched/application_standard_native_promotion.go` stays as it is.

**Adoption report.** A new vmmd RPC, specified here and built in slice 3. It
carries `{node, epoch, incarnation, predecessor_incarnation, adopted[], refused[],
resolved_ops[]}` and is the node's complete inventory. Schedd, the only writer
to `instances`, applies it as follows.

| Input | Schedd action |
|---|---|
| Adopted | No state change. App policies converge through schedd's durable policy loop (see slice 3). Admitted guests are never adopted (above). |
| Refused | `FAILED` with code `vmmd_adoption_refused` and the reason. The normal recreate path follows: wake on demand, or ADR-420 service recovery. |
| Live on this node in the database, absent from the report | Same as refused. The report is a full inventory, so this needs neither ADR-191's two-sweep wait nor its grace. |
| Resolved op | Apply the outcome from the table below. |

The node admits new wakes for an app only after that app's policies have been
re-pushed.

**Planned-restart notice.**

- A new column, `compute_nodes.vmmd_restart_notice_until`, is set by node_join.
- While the notice is active:
  - placement prefers other nodes;
  - wakes that hit the node are retried after the notice instead of failing;
  - the heartbeat counts no failures.
- The notice ends early when the adoption report arrives.
- A crash has no notice. vmmd's `RestartSec` drops from 15 s to 2 s, with the
  existing start-limit burst kept, so the gap stays well inside the 90 s window.

**Mid-flight operations.** The successor resolves every `op` intent it finds
before it reports. No resolution deletes a live snapshot or a rootfs, so
invariant 3 holds. A kill needs proof steps 2–5 (boot, process, cgroup,
network), even where the adoption block was never committed.

| Intent at restart | Resolution | Reported outcome |
|---|---|---|
| boot or restore, not yet ready | Verified kill through the pidfd, then full teardown and retire the record | `boot_rolled_back`. The wake RPC already failed `UNAVAILABLE`; the existing retry runs inside the wake hold. |
| ready, adoption block not committed | Not adoptable, so quarantine as today | `refused: uncommitted` |
| park (pause, capture, destroy) | Finish the destroy. schedd already treats the failed park RPC as a failed capture: it releases the ledger and moves the row to STOPPED, and the next wake restores the previous snapshot or cold-boots. Partial files are left to snapshot GC; no resolution deletes storage. | `stopped` |
| warm snapshot | Finish the destroy: schedd destroys the VM when a warm capture fails | `stopped` |
| freeze | Resume, even when the freeze commit was recorded (owner decision, 2026-10-18) | `thawed` |
| thaw | Resume (idempotent) | `thawed` |
| stop or destroy | Finish the teardown | `stopped` |
| migrate-out (source), prepare unanswered | Resume, as `CancelLiveMigration` would | `resumed` |
| migrate-out (source), prepare answered | Stays paused (`migration_paused`, written before the prepare answers); only the lease owner's `ResumeVM` or `Destroy` moves it | — |
| migrate-in (destination) | Commit acknowledged: adopt. Otherwise tear down. `migration_commit_recovery` applies. | `migrated` or `migration_cancelled` |
| policy update | Nothing: schedd re-pushes whole sets | — |

`thawed` always leaves a running guest, so it needs no FROZEN-to-RUNNING
repair in schedd:
- after an interrupted freeze, schedd's failed `FreezeInstance` already put
  the row back to RUNNING;
- after an interrupted thaw, schedd's failed `ThawInstance` already parked
  it;
- a FROZEN row that still meets a `thawed` guest (for example, schedd
  restarted too) is parked. Publishing it RUNNING needs the thaw path's
  secret and config fences, and parking a frozen app is safe (ADR-1151).

### 4. Release path: drain only for reboots

node_join's drain block at `node_join.yml:1138` becomes conditional.

1. **Capability check.**
   - The candidate binary prints `vmmd --print-adoption-capabilities`: readable
     record versions, adoption-block versions, guest-init protocol range,
     Firecracker API range and bridge protocol range.
   - The running vmmd reports its inventory through a local admin call. For
     each record it gives the version, kind, guest protocol, Firecracker version
     and whether it is adoptable.
   - The journal stays exclusively locked by its owner, so nothing else reads it.
2. **Skip the drain** when every live instance is adoptable by the candidate and
   the rollout needs no host reboot. node_join then:
   1. sets the restart notice (60 s);
   2. restarts vmmd on its own (`systemctl restart faas-vmmd`; the per-unit
      release session owns the order of the other daemons);
   3. waits for `gregalectl compute-nodes adoption-status --node … --wait` to
      report complete with zero refused;
   4. clears the notice.
3. **Partial drain.** Instances the candidate cannot adopt are migrated or parked
   alone before the restart (`drain --only-unadoptable`), not the whole node.
   Examples: a kind outside the adoptable set, or a pre-version-7 record.
4. **Full drain** remains for:
   - host or kernel reboots;
   - an explicit operator `--drain`;
   - a candidate that cannot read the journal;
   - nodes running `native_process_recovery`;
   - the first rollout onto the adoption-writing release, whose live records
     predate version 7.
5. **Firecracker upgrades do not drain.**
   - Adopted VMs keep their old Firecracker process.
   - vmmd keeps API compatibility with the previous Firecracker release for the
     calls it makes on adopted VMs: `GET /`, `PATCH /vm`, machine-config,
     balloon and snapshot create.
   - Parking an adopted VM whose recorded Firecracker version differs from the
     node's does not capture a snapshot that no current Firecracker can load. It
     destroys the VM, and the next wake cold-boots (ADR-005). That app's existing
     snapshot is already stale by `fc_version`.
6. **Rollback.** If adoption reports refusals above zero, or adoption never
   completes, the rescue reinstalls the previous release and restarts vmmd.
   That release adopts if it is adoption-capable, because §1 has every
   adoption-capable release read the records its successor writes. A rollback
   to a pre-adoption release quarantines and needs the drain. Refused
   instances are already being recreated by schedd.

### 5. Acceptance on fsn-4 (owner-approved)

**Fixture.** A node at 48 live VMs (about 3.5× production's per-node peak of
14), or at fsn-4's admission ceiling if that is lower. Shorter loops at 1, 14
and 32 VMs give the cost curve (§2). The mix includes:

- always-on services, functions and workers;
- one running job;
- one frozen instance and one quiet-paused instance;
- an egress-restricted app;
- a private-network pair;
- open WebSocket and SSE clients.

**Loop.** At least 50 iterations, rotating through:

- `systemctl restart faas-vmmd`;
- `kill -9` of vmmd (the crash path; systemd restarts it);
- a restart injected at each journaled phase of a wake, park, warm snapshot,
  freeze and stop.

**Every iteration must show:**

- zero refused;
- the same Firecracker PIDs;
- the same slots, UIDs, IPs and CIDs (invariant 5);
- unchanged `memory.max`;
- no row transition for an adopted instance;
- adoption p99 ≤ 2 s and gap p99 ≤ 5 s, never above 15 s;
- the job's exit delivered;
- console sequence continuous for file-backed consoles;
- egress still enforced: port 25 blocked, allowlisted host reachable;
- a liveness kill, a park, a wake and a destroy of an adopted instance all
  working and leaving no leak.

**Request continuity.**

- With today's vmmd forwarding, slice 5 measures and reports the failed-request
  fraction during the gap. In-flight forwards are cut, and later requests wait
  in the hold.
- With the connection-survival ADR in place, the target is zero failed
  requests.

**After the loop:** `make test-metal` and `make leakcheck` are clean, and once
every fixture instance is destroyed the resource journal holds no dead
records.

## Workload kinds

| Kind | Phase | Disposition at restart |
|---|---|---|
| App instance (service, function, worker) | slice 2 | Adopted |
| Job | slice 3b | Adopted. The job-exit listener (vsock 1026) is accepted by vmmd itself, independent of any RPC. The payload is journaled, and `WaitJobExit` returns it on re-call. Old guest-init retries for about 5 s in total (3 × 1.5 s ack timeout, `guest/init/job_supervisor_linux.go:702`). The stored listener covers that window. New guest-init retries until acknowledged, for at least 60 s. |
| App task, execution | slice 3b | Verified kill and teardown at restart (`resolved = killed`). Their I/O streams through vmmd break regardless, and both run at most once, so the coordinator reports `execution_transport_failed`. They no longer force a drain. |
| Builder | slice 3b | Verified kill and teardown (`resolved = killed`). builderd's export call ends with vmmd, so the in-flight build is lost, as without adoption. No regression, and no drain. |
| Environment qualification | — | Native mode only; unchanged |
| App instance on a claimed prepared network | 2026-10-22 | Adopted, like any app instance (§2 slice 2 note) |
| Prepared spare | — | Not adopted (§2) |

## Boundaries

- **Forwarding still runs through vmmd** until the connection-survival ADR moves
  it to forwardd and per-instance bridges. Until then a vmmd restart keeps every VM
  but cuts in-flight forwards. That meets "no drain" and not yet "zero failed
  requests".
  - New forwards no longer fail (added 2026-10-22). The full-stack restart
    test of 2026-10-19 lost 4 of 40 requests to `503 app_unavailable` during
    a 520 ms restart: a stream open to the stopped vmmd failed, and the
    gateway evicted a target whose guest was still running. The gateways'
    per-node vmmd client (`pkg/gateway/vmmd_restart_hold.go`, used by
    gatewayd-internal, tcpd and udpd) now retries a stream open that failed
    `UNAVAILABLE`. A stream open fails before any request byte is sent, so
    this is safe for every method.
    - Each retry resets gRPC's reconnect backoff, reconnects and waits up to
      100 ms for the channel. The successor clears the notice as it starts
      serving, while the channel can still be in the ~1 s backoff from the
      old socket; the first version, which only polled, released its held
      requests with 503 at exactly that moment.
    - While the node's planned-restart notice is active, retries continue;
      one more follows the end of the notice. Without a notice there is a
      single retry, so a crash still fails within about 100 ms.
    - The hold also ends with the request or after
      `api.GatewayVMMDRestartHoldSeconds` (the wake queue's 30 s), and then
      fails as before. Notice reads are cached for 100 ms, so the forwards
      that fail together share one read.
  - Verified 2026-10-22 on gregale-internal-test-1
    (`TestVMMDRestartKeepsRunningAppMetal`, prepared networks on, on a test
    merge of main as of 2026-10-19): restart 939 ms, 211 requests, 0
    failed; the guest was adopted (`adopted 1, refused 0`) and schedd
    applied the report under epoch 2; the successor parked and re-woke the
    app.
- **Outbound relay streams** (ADR-989/990), and guest connections to the
  runtime-config and workload-identity ports, are cut during the gap. Guests
  retry. A later slice either moves the outbound relay with the bridges or
  documents the cut.
- **`native_process_recovery`** keeps its strict quarantine and keeps draining.
  Converging the two journals is a follow-up.
- **Old guest-init and old bases** stay on adopted long-lived VMs. vmmd must
  support every guest-init protocol version that can still run on a node. A
  version below the candidate's minimum is a refusal, which means one recreate,
  not a drain. A stale-base rollover is a follow-up, like the ADR-1027 one:
  recycle idle long-lived VMs on old bases, a few at a time.
- **Exit codes** of adopted VMs are unknown (the pidfd watch is not the parent).
  Only diagnostics use them.
- **State main added after 2026-10-15 (merged 2026-10-22).** The inventory
  test (`adoption_state_inventory_test.go`) classifies it:
  - Rebuilt: gRPC readiness (`healthcheck_grpc` in the block) and the app's
    log ring. An adopted guest's ring now carries its app and deployment
    (`adoptedOwner`), as `registerLeaseRing` gives a booted one, so its
    lines still count as the app's and reach its log observers
    (ADR-743–752).
  - Refused: a rehearsal guest (`rehearsal_run_id` in the block, reason
    `rehearsal`). Its run-scoped outbound containment and recorder are not
    rebuilt, and a re-pushed policy must not drop the containment.
  - Killed: a data repair, which runs as an execution.
  - Not applicable: public forward targets and financial compute leases
    exist only with native process recovery; function VM resources only in
    native qualification builds, where adoption refuses function guests
    (`function_resources`).
  - Reset: console files being pre-opened, proven-worker marks (one full
    characterization window per deployment).
- **State main added after 2026-10-22 (split into PRs 2026-10-23).**
  - Refused: a production fork (ADR-732, `Lease.Quarantine`, reason
    `production_fork`). Its quarantine lives on `Instance.Net`, which the
    block does not carry, so a re-rendered netns would drop the fork's
    quarantine chains. Forks are short-lived; refusing one keeps today's
    quarantine.

## Invariants (§6.2)

1. **Concurrency.** Adoption creates no instance. Rolled-back boots only lower the
   count.
2. **RAM.** Adopted VMs keep their cgroups, and `memory.max` is verified. Schedd's
   admission ledger is untouched. Measured admission (ADR-909) re-reads the
   cgroups.
3. **Snapshot or rootfs.** Rollback discards only partial artifacts. Adoption
   never removes a live snapshot or a rootfs.
4. **Parked means zero RAM.** A completed or rolled-forward park destroys the VM
   and its cgroup. A rolled-back park returns the row to RUNNING.
5. **No shared identity.** Adoption re-leases each exact slot. Unproven records
   stay quarantined, and no slot is ever issued twice. A rolled-back boot
   releases its slot only after verified teardown.

Billing is unchanged. It still bills plan RAM + 8 MB per running second from
the rows (§4.7). Adopted rows keep billing; refused rows stop when schedd fails
them.

## Security (§11)

- No new root process.
- The journal stays root 0700 and holds no secrets.
- Every signal to an adopted VM goes through a pidfd.
- Adoption authority needs the full proof plus the epoch claim.
- Bridges stay unprivileged (connection-survival ADR).
- The `ProtectSystem=strict` sandbox is unchanged. The successor re-creates
  netns bindings in its own namespace. It never enters PID 1's mount namespace.

## Metrics and alerts (§12 names)

- `vmmd_adoption_instances_total{outcome}`, where `outcome` is `adopted`,
  `refused`, `rolled_back` or `rolled_forward`
- `vmmd_adoption_refused_total{reason}`
- `vmmd_adoption_duration_seconds`
- `vmmd_restart_gap_seconds`
- `vmmd_ownership_epoch`
- `schedd_vmmd_adoption_reconcile_total{outcome}`
- Alerts:
  - `FaasVmmdAdoptionRefused` (warn: any refusal in 10 min)
  - `FaasVmmdAdoptionFenced` (warn)
  - `FaasVmmdRestartGapHigh` (warn: > 15 s)

## Validation experiments (run first, on fsn-4, in slice 1)

- **V-A0.** Read-only, on every compute node: is `native_process_recovery` off in
  `/etc/faas/vmmd.toml`, and does `/var/lib/faas/vmmd-resources` hold the live
  records?
- **V-A1.** After a vmmd restart, are `/run/netns/<name>` bindings and image binds
  visible in the new vmmd's mount namespace? Does the old vmmd namespace stay
  alive while Firecracker runs?
- **V-A2.** Does the installed systemd keep the file descriptor store across
  `systemctl restart` and across a crash with `Restart=on-failure`?
  `FileDescriptorStorePreserve=` only exists from systemd 254.
- **V-A3.** Does jailer with an `O_APPEND` regular file as stdout and stderr work
  through exec? Does Firecracker survive a dead stdout pipe? The second is
  needed for VMs launched before the console change.
- **V-A4.** Can a non-parent `pidfd_open` Firecracker? Does `/proc/<pid>/exe` keep
  the original inode after the binary is replaced?
- **V-A5.** Does Firecracker's `GET /` report `Paused` and `Running` correctly to a
  new client?
- **V-A6.** Today's vmmd startup time per phase, to size slice 2's reordering.

**Results, fsn-4, 2026-10-17** (systemd 255, Firecracker 1.7.0, kernel 6.1;
`TestMetalAdoptionExperiments`; the old vmmd is a sandboxed transient unit,
SIGKILLed):

- **V-A1.** The successor does not see the predecessor's `/run/netns/<name>`
  binding (an empty file, not nsfs) or any of its image binds (0 of 3).
  Re-anchoring the binding is required. Firecracker's mount namespace is its
  own, not the old vmmd's.
- **nsfs inode reuse.** The successor's new mount namespace got the dead
  predecessor's inode number. Inode equality is not namespace identity over
  time. The bind-teardown holder check now ignores the caller's own
  namespace, and two live namespaces never share a number.
- **V-A2.** The FD store works on systemd 255: a stored listener came back as
  `LISTEN_FDS=1`, `LISTEN_FDNAMES=probe` after a crash restart and after
  `systemctl restart`.
- **V-A3.** Firecracker stays alive when its stdout pipe's reader dies, but
  its API stops answering (GET / and PATCH /vm timed out), so the guest
  cannot be adopted. This makes the console file a prerequisite: only
  guests launched with stdout on a regular file are adoptable.
- **V-A4.** A non-parent `pidfd_open` and signal 0 work.
- **V-A5.** Not measurable while the API was wedged (V-A3); repeat after the
  console file.
- **Dry run.** The slice 1 classifier wrongly refused the guest:
  `link_netnsid` was resolved through `/run/netns` names that the successor
  cannot see. The veth check now reads the guest side through
  `/proc/<pid>/ns/net` and compares the pair's ifindex and iflink from both
  ends.

## Slices

Each slice is one reviewable change on a branch. Slices 2–4 are behind
`adopt_running_vms` in vmmd.toml, default false, until slice 5 passes.

1. **Version-7 record, written in shadow; dry-run classifier; V-A0..6.**
   - Ships as reader R, then writer R+1 (§1).
   - Adds the adoption block, its validation, the off-critical-path readiness
     commit, op intents and the exe identity capture in `startJailer`.
   - At startup, `recoverRestartResources` classifies every record against the
     adoption proof and logs and exports
     `vmmd_adoption_instances_total{outcome}` with a `dry_run` label. Behavior
     does not change: quarantine stays as today.
   - Tests:
     - portable: parse and reject rules, version bumps, size bounds, crash
       windows around the async commit, older-reader rejection;
     - metal: a booted VM's record matches `/proc`, and a fresh Manager's dry
       run on the same journal classifies it adoptable.
   - Production soaks the dry run for one release before slice 2 is enabled.
   - **Built** on `feat/vmmd-no-drain` (`pkg/fcvm/resource_adoption*.go`,
     `cmd/vmmd/adoption_shadow.go`):
     - Every build reads version 7. Writing it needs `adoption_record = true`
       in vmmd.toml (default false), turned on only once every node and any
       rollback target reads version 7.
     - The block is computed from the live `Instance` and committed by one
       coalescing writer goroutine at readiness, framework-ready, freeze,
       thaw, warm-pool resume and policy-handle changes. A later block never
       loses to an earlier one. The writer re-checks the process incarnation
       around the exe stat, and the journal accepts the block only for the
       recorded process.
     - The exe identity is read from `/proc/<pid>/exe` at the first commit,
       not in `startJailer`: at `cmd.Start` the process is still the jailer,
       which execs Firecracker later under the same PID.
     - The dry run checks proof steps 1–6 (step 7 comes with slice 3), with
       a pidfd re-check on Linux. It runs in the background after startup and
       exports `vmmd_adoption_instances_total{outcome,dry_run}`,
       `vmmd_adoption_refused_total{reason,dry_run}` and
       `vmmd_adoption_duration_seconds{dry_run}`.
     - Op intents are written synchronously before snapshot, warm
       snapshot, park, freeze, thaw and stop effects and cleared when they
       complete (a failed stop keeps its intent). Migrate-out is the
       snapshot intent: the source side is `SnapshotKeepAlive` plus resume.
       The dry run refuses a record with an intent (`op_in_flight`) until
       slice 3 resolves them.
     - The veth-peer check compares the host veth's `link_netnsid` with the
       netns id of the instance's named namespace (`ip -j netns list-id`).
     - Still owed in this slice: V-A0..6 on fsn-4. Slice 2 must make the freeze and policy
       rewrites synchronous before the RPC acknowledges; in shadow they are
       asynchronous.
     - The block's schema is free to change until the first release that
       ships the reader; after that it follows the expand/contract rule.
2. **Cold adoption in vmmd:** §2 in full, refusals going to quarantine, the
   state-inventory reflection test, the startup reorder, the bounded stop,
   `FileDescriptorStoreMax`, the console file. It needs `test-metal` and
   `leakcheck`.
   - **Core built** on `feat/vmmd-no-drain` behind `adopt_running_vms`
     (requires `adoption_record`; `pkg/fcvm/manager_adopt.go`,
     `pkg/fcvm/vmm_adopt*.go`):
     - The startup pass reuses slice 1's proof, then applies the slice 2
       scope. Staged files (the reflinked writable layer, `clone`, and
       materialised copies and cache links) are adopted when their
       checkpointed identity still matches, so teardown removes them; the
       fsn-4 restart run showed every real guest has a clone. Warm-pool
       restores are adopted too (2026-10-18): the block records
       `warm_spare`, set by a paused restore and cleared by its first
       resume, because `Paused` stays set on a claimed warm instance. An
       unclaimed spare is adopted still paused with no probe loops, and
       `ResumeWarmInstance` claims it on the successor as before; a claimed
       one that was quiet-paused is resumed like any other. Private-network
       members are adopted too (2026-10-18): the block records the
       attachment (`network.private`: account bridge, address, CIDRs,
       allowed CIDRs, firewall rules), proof step 5 checks the side-link
       (`gpn-hNNNNN`) by ifindex and address, that it is still a port of the
       recorded bridge, and that its peer is in the guest's namespace
       (`private_link_mismatch` otherwise, also for a side-link the block
       does not describe), and adoption restores the attachment, the rule
       handles and the side-link's ownership. The fabric and transport maps
       are idempotency caches and start empty: schedd's private-network
       reconcile re-probes the kernel. A record with private rule handles
       but no attachment, written before this, is still refused. It refuses
       guests whose app has a static egress IP (`static_egress_ip`; the
       feature is off everywhere and its data path is unfinished, see
       ADR-119 "Known gaps", so this cannot fire today), guests booted under a
       runtime-admission receipt (`admission_receipt`: the receipt is not
       recorded, and without it a capture would skip ADR-595's artifact
       checks), assets other than jails, the netns, the host veth and image
       binds, and guests whose Firecracker jail path changed. Refused guests
       stay quarantined exactly as before.
     - A guest woken on a claimed prepared network (ADR-149) is adopted like
       any other (added 2026-10-22). Its record keeps the spare's storage key
       and creator boot; the claim had already moved the namespace binding
       and link to the guest's names, so the proof and re-anchor are the
       ordinary ones. This slice first refused such records
       (`prepared_network`), and the full-stack restart test of 2026-10-19
       showed the cost: production's cache of 4 prepared networks per node
       means most woken guests come from it and would still be drained.
       Only an unclaimed spare is refused now, and the classifier never
       reaches one.
     - For each adopted guest:
       - the quarantined slot is re-leased (`Allocator.adoptQuarantined`). A
         failure before registration returns it to quarantine, never to the
         free pool.
       - `/run/netns/<name>` is re-anchored from the verified process when
         this mount namespace lacks it, and the journal's netns checkpoint
         moves to this namespace.
       - The process gets a pidfd-backed handle (`os.FindProcess`) and a
         pidfd poll as its exit watch.
       - Bind bookkeeping and source-mode reference counts are rebuilt.
         Teardown removes the targets, restores the modes and retires the
         assets once no process holds the creator mount namespace.
       - The steady cgroup fence is written at once, which ends a startup
         CPU boost whose timer died with the predecessor.
       - The block now also records the egress allowlist, any static egress
         IP and the backing identity, so host SMTP rules and restorable
         captures survive adoption.
       - Guest channels re-bind their `v.sock_<port>` paths, and a quiet
         pause is resumed. Liveness, readiness and framework-ready loops
         restart.
     - `adoptionStateInventory` classifies every `Manager` and `JailerVMM`
       map field, and `adoptionInstanceInventory` every `Instance` field. A
       new field fails the test until someone classifies it. The `Instance`
       inventory found that adoption dropped an undeclared worker's observed
       class, which would have put it on HTTP liveness probes; the block now
       records `observed_class`.
     - With adoption on, vmmd's stop gives open RPCs 2 s, then ends them
       with UNAVAILABLE instead of waiting up to the unit's 90 s timeout.
     - Console file built: with `adoption_record` on, Firecracker's stdout
       and stderr are the console file itself (`O_APPEND`). vmmd tails it into
       the ring, and an adopted guest's tail resumes at the file's current
       end. The block records `console_file`, and pipe-console guests are
       refused (`console_pipe`).
     - Still owed: the console catch-up offset (lines written while no vmmd
       ran stay in the file but not in the ring), the FD store (V-A2 shows
       systemd 255 keeps it) and the startup reorder (V-A6). The fsn-4 real
       restart passed in run 4 (slice 3). `RestartSec` 15 s to 2 s moves to slice 4: with the existing
       5-in-60 s start limit, a crash-looping vmmd would then stop
       restarting instead of retrying.
3. **Epoch, report and reconciliation:**
   - the migration (`vmmd_epoch`, `vmmd_predecessor_incarnation`,
     `vmmd_restart_notice_until`, with CHECKs);
   - the adoption-report RPC (a vmmdgrpc and proto change, coordinated with the
     connection-survival session, which is editing vmmdgrpc);
   - schedd's refused-to-recreate (the receipt re-bind and policy re-push
     turned out unnecessary or infeasible; see below);
   - mid-flight resolution.

   **3b** adopts jobs.
   - **Epoch built** on `feat/vmmd-no-drain` (slice 3a): the migration,
     `ComputeNodeVMMDEpoch`/`ClaimComputeNodeVMMDEpoch`, the journal's
     `.epoch` file, and `claimOwnershipEpoch` in `cmd/vmmd`. A fenced claim
     still holds the new epoch and adopts nothing.
   - **Report built** (`AdoptionReport`, `pkg/vmmdgrpc/adoption_report.go`):
     node, epoch, incarnation, predecessor, `fenced`, adopted and refused
     guests, and `leased_since_start`, every instance this vmmd gave a slot
     after startup. The lease set is the fence the "absent" row needs:
     schedd lists the node's rows first and fetches the report second, so a
     guest the successor created by the list is named in it. The set is
     bounded (65,536); an overflowed report says `complete = false` and
     schedd acts on nothing in it. UNIMPLEMENTED means adoption is off,
     UNAVAILABLE that this start claimed no epoch.
   - **schedd's refused-to-recreate built** (`pkg/sched/vmmd_adoption_report.go`):
     every 5 s (`api.AdoptionReportIntervalSeconds`) schedd reads each
     node's epoch, and on a new one applies the report once, off the loop.
     A RUNNING row the vmmd does not hold goes through ADR-419's repair
     (app lock, reread, vmmd cleanup, conditional `FAILED`, audit reason
     `vmmd_adoption_refused`, routing and service/worker recovery); the
     reread must still show the listed wake and start. A FROZEN row parks
     (ADR-1151). Waking, cold-booting, snapshotting, migrating and draining
     rows stay with their controllers, with ADR-419 as the backstop. A
     report whose epoch or incarnation differs from the row's waits for the
     next tick.
   - The ADR-740 live-patch mark is recorded in the block
     (`instance.dev_patched`) before the patch is served and restored for
     adopted guests, so they stay unsnapshottable.
   - Policy re-push, as found while building it: egress and CPU policy
     already converge durably. `EgressDriftSubscriber.reconcilePending`
     re-pushes every target whose observation is stale or whose revision
     differs, every 30 s, so a change made while no vmmd ran converges on
     an adopted guest within that interval. An adopted guest's nft rules
     are kernel state, and its allowlist comes back from the block.
     DNS-gated entries (ADR-373) expire by their nft timeouts; vmmd's
     copies are caches, so losing them costs extra nft calls only. Static
     egress IPs stay refused. A report-triggered push
     for the adopted apps would only shorten the 30 s.
   - **Jobs adopted** (2026-10-18). vmmd accepts a job's exit frame itself,
     from the moment the listener is bound, instead of inside a
     `WaitJobExit` call. The frame is written into the block
     (`instance.job.exit`) before the guest is acknowledged, and
     `WaitJobExit` returns it however many times it is called; ending a
     call leaves the receiver running. The block is committed when the job
     is published (a job has no readiness) and records the start gate and
     whether the job is interactive. An adopted job gets its receiver back
     on the listener the predecessor stored, whose backlog holds any frame
     sent while no vmmd ran; old guest-init gives up after three attempts,
     but its queued frames are still read. Without a stored listener the
     path is re-bound. A job whose exit was recorded gets no receiver. An
     interactive job's attach stream runs through vmmd and breaks, as app
     tasks' do. Qualification jobs stay refused (`kind`).
     New guest-init retries its exit frame until acknowledged for 60 s
     (`jobExitShipBudget`, backoff 100 ms doubling to 2 s, at least the old
     three attempts), which spans a restart whose successor re-binds. On
     the scheduler side, `VMMRouter.WaitJobExit` retries an UNAVAILABLE wait
     under the node's restart notice, as creates do, so the job's exit
     watch re-attaches to the successor instead of failing the task as
     `infra`. A call that just failed re-reads a notice cache older than
     100 ms, so a notice set within the last second still counts. A crash
     sets no notice: the wait fails as before and the task retries.
   - **App tasks and executions killed** (2026-10-18), as the workload
     table decided. Their command and result travel over one stream the
     predecessor held, so after the full proof the successor kills them
     through the ordinary teardown (pidfd kill, resources freed, record
     retired) and reports them not held with `resolved = killed`, counted
     apart from refusals and stops (`vmmd_adoption_instances_total{outcome="killed"}`).
     Both coordinators already treat a broken stream after the running
     fence as terminal (`execution_transport_failed`): they run at most
     once, so nothing is replayed. What changes is the release: the
     pre-restart check lists them under `killed` and no longer counts them
     as unadoptable, so one running task no longer drains the whole node
     (a drain would end it too). Disposable executions are networkless; the
     scope and the adoption now allow a lease without a namespace or veth.
     `VMMRouter.Destroy` retries under the restart notice, so a task's
     teardown during the gap does not withhold its terminal
     acknowledgement until the lease expires.
     **Fixed: builders were recorded as apps.** A builder boots through
     Wake with an export directory and got a block of kind `app`, so a
     successor would have adopted it as an app and left an ownerless
     builder VM running. The block now says `builder`, and builders are
     killed like app tasks; builderd's export call ended with the
     predecessor, so the build fails as it would have without adoption.
   - **A planned stop flushes the block writer** (2026-10-18). Rewrites
     are asynchronous, so a change made just before the stop (an
     operation's end, a policy patch, an attachment) could miss the record
     the successor reads; an ended warm snapshot would then resolve as cut
     short and destroy a healthy guest. vmmd writes every pending block
     once its RPCs have ended (`FlushAdoptionRecords`). A crash keeps the
     window, which fails safe: a stale intent finishes the teardown and the
     owner retries.
   - **Freeze and thaw intents resolved**: the adoption pass resumes the
     guest, closes its frozen swap, adopts it running and reports
     `resolved = thawed`; schedd parks a FROZEN row that meets one.
   - **Stop and destroy intents resolved**: a proven guest whose stop was
     cut short is adopted without resuming anything and destroyed through
     the ordinary teardown, which retires its record and frees its slot. It
     is reported as not held with `resolved = stopped` and counted apart
     from refusals (`vmmd_adoption_instances_total{outcome="stopped"}`), so
     slice 4's refusal rollback does not fire on it. A teardown failure is
     `teardown_failed` and keeps the retained cleanup.
   - **Park, warm-snapshot and migration-prepare intents resolved** as in
     the table: park and warm snapshot finish the destroy (`stopped`); an
     unanswered prepare resumes (`resumed`).
   - **Fixed: a paused migration source was resumed by adoption.**
     `SnapshotKeepAlive` leaves the source paused for the lease owner, and
     slice 2 took that for a quiet pause. The block now records
     `migration_paused` synchronously before the prepare answers (a failed
     write fails the capture, which resumes the guest), the successor adopts
     the guest still paused, and an owner resume clears the mark.
   - **Metal, fsn-4 run 4 (2026-10-17, `e37006177d`)**: V-A experiments,
     the dry run over a live guest and a real restart all pass. A separate
     successor vmmd process adopted a live guest in 18 ms (same PID,
     Firecracker `Running`, netns binding re-anchored), then destroyed it
     through the ordinary path and retired its record; leakcheck clean.
   - Admission receipts are not re-bound (owner decision, 2026-10-18; see
     "Runtime-admission incarnation"): admitted guests stay refused.
   - Deferred: the capacity-telemetry epoch fence. systemd runs one vmmd per
     node and starts the successor only after the predecessor exits, so two
     inventories can only overlap if an operator runs a second vmmd by hand.
4. **node_join without the drain:** the capability check, the partial drain, the
   restart notice and the rollback rule, coordinated with the per-unit
   release-pipeline session.
   - **Capability check built (slice 4a)** as two local subcommands over a
     root-only admin socket (`/run/faas-ibridge/vmmd-admin.sock`, mode 0600,
     every connection's SO_PEERCRED checked), in place of
     `--print-adoption-capabilities` plus a separate inventory call:
     - `vmmd adoption-check`, run with the **candidate** binary before the
       restart. It fetches the running vmmd's journal records and live proof
       (`AdoptionInventory`, steps 2–6) and judges each record with its own
       strict journal decoder, step 1 and the adoption scope. A record it
       cannot decode (a newer release's, say), a refused scope, a failed
       live proof, a candidate config without `adoption_record` and
       `adopt_running_vms`, `native_process_recovery`, or a running vmmd that
       cannot answer all mean drain. Exit 0 adopt, 3 drain, 1 the check
       broke.
     - `vmmd adoption-report --wait`, run after the restart, in place of
       `gregalectl compute-nodes adoption-status`: it needs no control-plane
       call. Exit 0 for a complete, unfenced report with zero refused
       (finished stops do not count), 3 otherwise (roll back), 1 when no
       report arrived in time.
     - The ordinary listener refuses `AdoptionInventory`
       (`PERMISSION_DENIED`): the records carry every guest's identity.
   - **Restart notice built (slice 4b).** vmmd itself writes it, not
     node_join: an adopting vmmd that stops gracefully sets
     `vmmd_restart_notice_until` to 60 s ahead on the database clock
     (`api.VMMDRestartNoticeSeconds`), and its successor clears it as soon
     as it serves. A crash sets none. schedd reads active notices through a
     1 s cache and, for a node under one:
     - tries the other nodes first in placement, falling back to all of
       them;
     - retries a cold-boot or restore create that finds the node's vmmd
       `UNAVAILABLE` every 250 ms until the notice ends, bounded by the
       wake's own deadline;
     - does not count failed heartbeat pings, so a restart never trips the
       three-failure fail-over.
   - **node_join without the drain built (slice 4c).** For an existing node
     with a running vmmd, node_join runs the candidate's `adoption-check`
     from the verified pre-stage tree before the drain block. Exit 0 skips
     the drain and the early stop of the compute daemons, so every daemon
     keeps serving through the install. The usual "restart the compute-only
     daemon set" then restarts vmmd, which adopts its guests, and
     `adoption-report --wait 90s` must come back clean. Anything else keeps
     today's drain. `faas_join_force_drain` keeps it on purpose.
     - Deviations: there is no automatic rollback yet. The compute lane has
       no rescue path, so a dirty report fails the rollout loudly while
       schedd recreates the refused guests. The partial drain
       (`--only-unadoptable`) is not built: one unadoptable guest still
       drains the whole node. The check reads the node's current vmmd.toml,
       not the candidate's rendered one.
5. **fsn-4 acceptance** (§5), then flip `adopt_running_vms` to true by default.

## Risks

- **Reversing ADR-472–475's core rule.**
  - **Risk:** a bug could adopt a foreign or reused process, or give cleanup
    authority over something vmmd does not own.
  - **Mitigations:** the multi-factor proof, pidfds, the boot and epoch fences,
    a one-release dry-run soak, and the flag defaulting to off.
- **Mount namespaces.**
  - **Risk:** if V-A1 shows the successor cannot see netns bindings, every
    `ip netns exec` on an adopted instance fails until it is re-anchored. That
    includes today's bridge start, so forwarding fails too. The re-anchor
    therefore belongs in slice 2, not later.
- **systemd version.**
  - **Risk:** without file descriptor store preservation, guest connects fail
    during the gap. A job exit from an old guest-init is lost if the gap exceeds
    about 5 s.
  - **Mitigation:** re-bind is the fallback, and the gap budget is 5 s.
- **Startup slowness.** One slow startup step can break the gap budget, as the
  137 s spool walk did. Mitigations:
  - the reorder;
  - a test that fails if pre-serve startup work grows unbudgeted;
  - `vmmd_restart_gap_seconds` alerting.
- **Hidden state.** In-memory state that is not adopted misbehaves only after a
  restart.
  - **Mitigation:** the reflection inventory test forces every new map field to
    be classified.
- **Skew.** Mixed Firecracker and guest-init versions accumulate on long-lived
  VMs.
  - **Mitigations:** the declared compatibility ranges, refusal leading to a
    recreate, and a stale-base rollover follow-up.
- **Two journals.** The default-mode resource journal and the native launch
  journal diverge further. Native mode keeps draining until they converge.
- **Gap requests.** Requests during the gap still fail until forwarding leaves
  vmmd. The owner's zero-failed-request goal depends on the connection-survival
  ADR.

## Rejected alternatives

- **A faster drain** (parallel migrations). Still minutes per node, still a
  capture and restore of every VM, still cold paths. It misses the goal.
- **A per-VM supervisor process** (the containerd-shim model).
  - It is the most robust long term, but it adds a privileged process per VM,
    against §11's single root component.
  - It moves almost all `JailerVMM` state into a new daemon that needs its own
    upgrade-and-adoption story.
  - The unprivileged per-instance bridge is the part of that model this design
    keeps.
- **In-place re-exec as the only mechanism.** It covers neither crashes nor unit
  and environment changes. It is kept as a possible optimization (§2).
- **Carrying the incarnation over.** The predecessor's replay sets would have to
  become durable, and a stolen incarnation would grant boot authority.
  Refusing admitted guests keeps the incarnation per-process.
- **Rebuilding from observation alone** (`/proc`, the Firecracker API) without a
  journal. Observation cannot give app, account or deployment identity,
  receipts or op intents, and ADR-472 showed it cannot prove ownership.
- **Building on `native_process_recovery`.** It is off in production. Turning it
  on changes pmem, prepared networks and image staging. This design uses the
  journal that production runs.
