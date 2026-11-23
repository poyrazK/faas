# ADR-20261122132055 · Persistent sandbox sessions: memory-preserving pause, fork, workspace volumes, large shapes and moves

- **Status:** proposed
- **Date:** 2026-11-22
- **Phase:** ADR-20261027090014 §5 phase 2, in one ADR (owner's choice, 2026-11-22).
- **Amends:**
  - ADR-20261027090014 §5: phase 2 is this ADR.
  - ADR-20261027090013 §1 (sessions of kind `persistent` are admitted), §2 (a persistent session's workspace is a volume), §5 (a persistent session pauses where an ephemeral one expires), §7 (new limits), §12 (the shims' `betaPause`, `pause`, `resume` and `fork`).
  - ADR-382 for persistent sessions: their workspace is a volume, not an archive.
- **Builds on:** ADR-005 (cold boot always works), ADR-171 (a restore gets a new identity), ADR-159 (immutable captures), ADR-063 (snapshots move between nodes through object storage), ADR-732 and ADR-733 (forks of a running VM), ADR-1151 (frozen state), ADR-20261110210000 (sealed capture keys), ADR-20261027090015 (compress-on-wait, measured reservation), `20261017-vmmd-restart-adoption.md` (adoption).

## Context

Sessions today are ephemeral. They live for at most `SandboxMaxLifetimeSec` (1 h / 4 h / 24 h). They freeze after 30 s idle and expire after 10 minutes idle, losing their memory and, unless it was persisted as an archive, their workspace. Users moving from E2B and Daytona ask for four things this cannot do:

1. **Pause and resume with memory kept** (E2B `betaPause`, Daytona stop/start). Long-lived agents keep a running dev server, a loaded model or a REPL's state across hours or days of no calls.
2. **Fork.** Evals, RL rollouts and agent tree search start many sessions from one prepared state.
3. **Large, long sandboxes.** Builds and data work need 16–32 GB and 8–16 vCPU for days.
4. **A workspace that is a disk,** not an archive: tens of GB that survive restarts without a tar round trip.

The parts exist. Firecracker captures and restores a VM's memory. Captures are sealed at the source, account-bound and immutable (ADR-159, ADR-20261110210000). Restores reseed entropy and step the clock (ADR-171, invariant 5). ADR-732/733 restore a running VM's capture into a new instance. The frozen state (ADR-1151) already keeps a session's memory on its node while it idles. What is missing is a session that outlives its VM.

## Decision

### 1. Session kind and states

- A create with `kind: "persistent"` is admitted on plans with `SandboxPersistentMax > 0` (§6).
- **New state `paused`.** `ready ⇄ frozen` as today, plus `ready | frozen → pausing → paused → resuming → ready`. `closed`, `expired` and `failed` stay terminal. agentd owns every transition (ADR-20261027090014 §1); schedd places and owns the instance; vmmd captures and restores.
- **A paused session has no VM.** Its cgroup, jail and netns are gone, as for a parked app (invariant 4). It holds a **memory snapshot** (§2) and a **workspace volume** (§4).
- **Pausing:**
  - explicitly, with `POST /v1/sandboxes/{id}/pause`;
  - after `SandboxPersistentIdlePauseSec` with no calls, where an ephemeral session would expire;
  - when the session's running time reaches `SandboxPersistentRunMaxSec`;
  - for a node drain (§7).
- **Resuming:** explicitly with `POST /v1/sandboxes/{id}/resume`, or implicitly on the first data-plane call to a paused session. That call waits up to `SandboxResumeWaitSec` and otherwise answers 503 with `Retry-After`. `GET` re-mints the token, as today.
- **Ending.** A persistent session ends at `DELETE` (closed) or when it has been paused for `SandboxPausedRetentionDays` (expired, outcome `retention`). Its snapshot and volume are deleted then. There is no total lifetime cap.
- **Ephemeral sessions are unchanged.**

### 2. Memory-preserving pause

- **Capture.** vmmd pauses the VM and captures memory and device state with the same code path as warm and crash captures. The capture is sealed at the source to the runtime domain, bound to the account (ADR-20261110210000), and immutable (ADR-159). Memory is compressed as on wait (ADR-20261027090015 §2). The workspace volume is flushed and its point-in-time state recorded with the capture (§4).
- **Storage: the node first, then object storage** (owner's choice). The capture stays on the node's disk, so a resume there needs no download. A background copy uploads it to object storage under `snap/sandbox-session-<account>/captures/<capture>/v2/{mem,vmstate,drive,backing}` (the v2 capture layout, so drive1 is coupled; the row in `sandbox_session_captures` names the session).
  - Until the upload completes, the session is `paused` with `durability: node`. After it, `durability: remote`.
  - A node copy is dropped under the node's snapshot cache pressure (ADR-159's eviction), never before the remote copy exists.
- **Resume.**
  - On the node that holds the capture when it has room. Otherwise anywhere on the agent pool from the remote copy, which then costs a download of a few seconds per GB.
  - The restore takes a **new instance identity** (ADR-171, invariant 5): IP lease, netns, jail uid, reseeded entropy, a stepped clock. The session keeps its ID; tokens are re-minted.
  - guest-init's resume hook runs as for any restore. Network connections the guest held are gone, as after any pause; listening sockets survive.
- **A capture is used once.** Resuming consumes the capture, and the next pause makes a new one. Nothing else may restore a session capture; fork has its own (§3).
- **Cold boot still works (ADR-005).** A capture that cannot be restored (lost, stale Firecracker version, a CPU the node cannot carry, a failed integrity check) resumes the session by cold-booting its image on its workspace volume, and the session reports `resumed_cold: true`. Memory state is lost then, never the volume. **Invariant (new, enforced like invariant 3):** a paused session always has a restorable capture or a backed-up workspace volume, never neither.
- **Diffs.** After the first full capture, later pauses capture only the pages written since the last restore (Firecracker diff snapshots), merged into a full image in the background. This shortens pause and upload; restores always use a full image.

### 3. Fork

- `POST /v1/sandboxes/{id}/fork` with `{count, labels?, kind?}` (count 1 to `SandboxForkMax`) answers `{forks: [{id, state, token}]}`. The source must be `ready`, `frozen` or `paused`.
- **One capture, N restores.** A running or frozen source is captured once without being stopped: the ADR-733 live capture, so the source pauses only for the capture itself. A paused source's capture is used as it is. Each fork restores that capture into a **new session** with:
  - its own session ID and token;
  - a new instance identity (IP, netns, jail uid, reseeded entropy, a stepped clock);
  - a copy-on-write clone of the source's workspace volume at the capture point (§4).
- **What a fork does not inherit:** exposed ports and preview tokens, background command handles (their processes run in the fork too, but the handles are the source's), and the session env set with `PUT …/env`. The env lives in memory, so the fork has it; that is called out to callers, since a secret set in the source reaches every fork.
- **Kind.** Forks are `ephemeral` unless `kind: persistent` is asked for and the plan allows it.
- **Limits.** Forks count against `AgentRunConcurrentPerAccount` and the persistent limits like any session. A fork request that would pass a limit is refused whole, not partly made.
- **Restore contract.** The source capture is taken after the session ran tenant code, so, unlike preset warm snapshots, forks share memory with each other: files in `/tmp`, caches, and userspace RNG state seeded before the capture. The resume hook reseeds the kernel RNG and the clock. The docs tell callers that a process holding its own PRNG state needs to reseed it after a fork; a `GREGALE_FORKED_FROM` marker in the guest environment and a `SIGUSR2` to guest-init's children (opt-in per command) let them. This is a documented difference from invariant 5's wording, which covers platform identity (IP, netns, jail uid, kernel RNG) and still holds.

### 4. Workspace volumes

- **A persistent session's `/workspace` is a volume:** an ext4 block device on the node's `/srv/fc` (XFS, reflinks), sized at create within `SandboxVolumeMaxGB` (default 10 GB). Guests get block devices only, never shared host directories.
- **Backed up to object storage.** On every pause, and every `SandboxVolumeBackupSec` while running, vmmd uploads the blocks changed since the last backup, using a changed-block map kept from the volume's reflink snapshot. The backup is sealed and account-bound like captures. A volume is restorable from its newest backup when its node copy is gone.
- **Point in time.** A pause's capture names the backup that matches it, so a cold resume and a fork get the workspace as it was at the capture.
- **Fork** clones the volume with a reflink on the source's node, or restores the backup elsewhere.
- **Ephemeral sessions** keep the scratch drive and `workspace:persist` archives (ADR-382). `workspace:persist` also works on a persistent session and makes an archive snapshot as today.
- **Growing a volume:** `PATCH {volume_gb}` grows it while paused, never shrinks; the next resume is cold.

### 5. Large shapes

- `ram_mb` up to `SandboxPersistentRAMMBMax` and `vcpu` up to `SandboxPersistentVCPUMax` for persistent sessions: 16 GB and 8 vCPU on Scale; 32 GB and 16 vCPU by contract.
- They run only on agent-pool nodes that declare the capacity (ADR-20261027090014 §2), under measured admission (ADR-20261027090015 §3). A request no node can place queues and fails after `SandboxQueueMaxSec` with `capacity_unavailable`, as other sessions do.
- **The 47,600 MB ceiling (invariant 2) is per node and unchanged.** A 32 GB session takes most of a node's tenant budget. The agent pool's nodes are sized for it, and the scheduler packs large sessions before small ones on a node chosen for them.

### 6. Limits (pkg/api/limits.go)

Proposed values; the owner confirms them with the prices (§8).

| Name | Free / Hobby / Pro / Scale |
|---|---|
| `SandboxPersistentMax` (paused + running, per account) | 0 / 2 / 10 / 50 |
| `SandboxPersistentIdlePauseSec` | — / 300 / 600 / 1,800 |
| `SandboxPersistentRunMaxSec` (running before an automatic pause) | — / 86,400 / 259,200 / 604,800 |
| `SandboxPausedRetentionDays` | — / 7 / 30 / 90 |
| `SandboxResumeWaitSec` | 30 |
| `SandboxPauseTimeoutSec` (a pause vmmd never answers; a capture landing up to 3× later is still taken) | 600 |
| `SandboxPersistentOverrunSec` (running past the run limit while every pause is refused, then the session ends) | 1,800 |
| `SandboxCaptureUploadSec` (a node uploads a capture this long, then leaves its copy to eviction) | 86,400 |
| `SandboxResumeRetrySec` (a resume refused for want of its capture's node is claimed again after) | 30 |
| `SandboxForkMax` (per request) | 0 / 1 / 10 / 100 |
| `SandboxVolumeMaxGB` | — / 10 / 50 / 200 |
| `SandboxVolumeBackupSec` | 600 |
| `SandboxPersistentRAMMBMax` | — / 2,048 / 8,192 / 16,384 (32,768 by contract) |
| `SandboxPersistentVCPUMax` | — / 2 / 4 / 8 (16 by contract) |
| `SandboxPausedBytesFreePerAccount` (§8) | — / 5 GB / 25 GB / 100 GB |

### 7. Moves, drains and restarts

- **vmmd restart.** Persistent sessions are adopted across vmmd restarts as `20261017-vmmd-restart-adoption.md` describes for apps: the VM keeps running, vmmd rebuilds its handles, agentd's lease survives. This ADR makes adoption a prerequisite for admitting persistent sessions. Until adoption ships, a vmmd restart pauses every persistent session first (§2) and resumes them after, which costs a capture and a restore each but loses nothing.
- **schedd and agentd restarts** keep their leases and reattach to the running stream; they never end a persistent session.
- **Drain.** A node drain pauses each persistent session on it, waits for its remote copy, and resumes it on another node (or leaves it paused if it was idle). An ephemeral session on a draining node ends, as today.
- **Node loss.** Sessions resume from their remote capture and volume backup. A session whose capture had not reached object storage resumes cold from its newest volume backup (§2, `resumed_cold`), so it can lose up to `SandboxVolumeBackupSec` of workspace writes, which the docs say.

### 8. Billing

- **Running:** as for every session (ADR-20261027090013 §5, step 11). Average private memory by the conversion `k`, with quiet-paused and frozen seconds unbilled (D15).
- **Paused and stored (owner's choice): per GB-month.** A session's stored bytes are its compressed memory capture (remote copy) plus its volume backup, deduplicated across a fork tree's shared base. Each account gets `SandboxPausedBytesFreePerAccount` free. Bytes above it are billed per GB-month, metered hourly by meterd (`sandbox_storage` meter kind) and shown on statements.
- **Volumes** are counted at their allocated size while running and at their backed-up size while paused.
- **Fork:** each fork is a session and bills as one; the shared capture is stored once.
- **The prices** (GB-month, and `k`) go into the financial model and pkg/api's price table. They are an open question tied to the `k` decision.

### 9. API and clients

- **Routes** (apid; control plane):
  - `POST /v1/sandboxes/{id}/pause`
  - `POST /v1/sandboxes/{id}/resume`
  - `POST /v1/sandboxes/{id}/fork`
  - create takes `kind`, `volume_gb` and `vcpu`
  - `PATCH` takes `volume_gb`
- **The view** adds `paused_at`, `durability` (`node` / `remote`), `resumed_cold`, `forked_from` and `stored_bytes`. `GET /v1/sandboxes?state=paused` lists paused sessions.
- **E2B shims:**
  - `Sandbox.create({ kind: 'persistent' })` (Gregale option);
  - `sandbox.betaPause()` and `pause()` map to `/pause`;
  - `Sandbox.connect(id)` resumes a paused session, as E2B's `connect` does;
  - `sandbox.fork(n)` is a Gregale extension.

  The current refusal of `betaPause` and `pause` goes away for persistent sessions and stays, with its message, for ephemeral ones.
- **Agents SDK client:** `persistSnapshot` stays the workspace archive. Persistent sessions are created with `options.kind`, and `resume` reattaches to a paused session.
- **CLI:**
  - `gregale sandbox pause|resume|fork <id>`;
  - `gregale sandbox ls --paused`.

### 10. Security

- Captures and volume backups are sealed at the source and bound to the account. vmmd restores a session capture only into a session of the same account (`storagekey.IsSandboxSessionSnapshotKey` with the account in the key, checked against the session row as template snapshots are today).
- A paused session's capture holds whatever the guest held: secrets set with `PUT …/env`, tokens in process memory. It is protected like production crash captures (ADR-733): sealed, account-bound, never readable by operators without a support access grant (ADR-20261116163508).
- Fork copies all of that into every fork (§3). The fork route is behind the session's API key, not its data-plane token, so a process inside a session cannot fork it.
- Root inside the guest (ADR-20261027090013 §10) is unchanged. The no-module guest kernel is a prerequisite here too.

### 11. Ownership

- apid writes intents (pause, resume, fork requests) on the session row.
- agentd owns transitions, timers (idle pause, run max, retention) and fork fan-out.
- schedd places restores, fork restores and drains.
- vmmd captures, uploads, restores, backs up volumes, and clones on reflink.
- meterd meters storage.
- New tables:
  - `sandbox_session_captures`, written by agentd from vmmd's reports;
  - `sandbox_volumes` and `sandbox_volume_backups`.

  Migrations are expand-only, as before.

## Metrics (§12 additions)

- `sandbox_session_pause_seconds`, `sandbox_session_resume_seconds{source=node|remote|cold}`
- `sandbox_session_captures_bytes`, `sandbox_session_capture_upload_seconds`
- `sandbox_fork_seconds{count_bucket}`, `sandbox_forks_total{outcome}`
- `sandbox_volume_backup_seconds`, `sandbox_volume_backup_bytes`
- `sandbox_paused_sessions`, `sandbox_stored_bytes`
- `sandbox_resumed_cold_total{reason}`

## Rollout

Each step is one PR on the sandbox branches until the owner asks for PRs. Every VM-lifecycle step runs `make test-metal` and `make leakcheck`.

1. This ADR, the limits in `pkg/api/limits.go`, and the financial-model update for GB-month storage (with `k`).
2. Migrations, the `paused`/`pausing`/`resuming` states, and the apid routes, refusing until agentd serves them.
3. vmmd: session capture and node-local restore with a new identity; agentd pause and resume on one node. Metal: pause, resume, state kept, new IP, reseeded RNG, leakcheck clean.
4. Upload to object storage, `durability`, remote resume on another node, cold resume from the volume (`resumed_cold`).
5. Workspace volumes: create, grow, changed-block backups, restore from backup.
6. Fork: live capture, N restores, reflink volume clones, the fork limits.
7. Drains and restarts: drain moves; schedd and agentd reattach; the vmmd-restart pause fallback, then adoption when it ships.
8. Large shapes on agent-pool nodes that declare them.
9. Storage metering and statements.
10. Clients: shims (`pause`, `resume`, `connect` resumes, `fork`), the Agents SDK client, the CLI, docs, and the `TestSandboxes` journey extended to pause, resume and fork.

### Step 3 as built

- **Capture.** vmmd pauses with `Manager.Park`, the terminal capture apps park with, under the account's session namespace. A pause with a call in flight is refused, and calls are refused while it runs. A capture that fails before the guest is touched leaves the VM running (`pause_refused`); one that fails after it loses the VM, which agentd records as a lost VM.
- **Publication.** Superseded by step 4 below.
- **Resume.** A paused session asked to resume is claimable; schedd prefers the capture's node. A resume that never booted goes back to `paused` (its request kept for transient failures). Without volumes (step 5), a cold resume starts from a fresh workspace.
- **Timers.** A running persistent session pauses on request, at `SandboxPersistentRunMaxSec` and after `SandboxPersistentIdlePauseSec` without calls. A paused session's `expires_at` is its retention end.
- **Not yet:** the implicit resume on a data-plane call (calls to a paused session fail until it is resumed); usage across a resume (the pause's usage record is final, so step 9 meters resumed VMs).

### Step 4 as built

- **Node first.** The pause writes every capture object (mem, vmstate, drive, backing) to the node's snapshot cache only, each with a `.local` mark that pins it against cache eviction. A pause no longer waits for object storage.
- **Upload.** vmmd then copies the capture to the cache's parent store in the background and drops the marks, retrying with backoff (2 s up to 5 min, giving up after 24 h); a vmmd restart publishes whatever is still marked. Its duration is the `vmmd` operation `SandboxCaptureUpload` (standing in for `sandbox_session_capture_upload_seconds`). A capture deleted while it uploads is deleted again from the parent.
- **Durability.** agentd's sweep turns `durability: node` into `remote` once the shared store holds every object of the capture (the backing only when the pause reported one, `has_backing`). The capture's row is the record of it.
- **Resume.** A `remote` capture resumes on its node when it has room and anywhere on the pool otherwise. A `node` capture resumes only on its node (`require_node`): schedd answers `capacity` when it is busy, draining or restarting, and agentd retries after `SandboxResumeRetrySec`; only a capture whose node is gone resumes cold.
- **Capture cleanup.** A capture is used once: by its resume, or when its session ends, from `resuming` too. A used capture's objects are deleted only once the shared store holds all of them or `SandboxCaptureUploadSec` (plus an hour) has passed, so no late upload outlives its row. A pause agentd never saw answered (its stream broke, or it gave up on a late capture) is recorded as a used capture under its key for the same sweep. An upload that gives up, or one a restart finds past that window, unpins the node's copy, which the cache may then evict. The sweeps take the least recently checked capture first.
- **Local paths.** A node-first capture object is local-only until its upload, so the node cache resolves it to its own copy. A local-disk shared store names a path for every key, and restores before the upload used to stage from that missing path and cold-boot.
- **Not yet:** captures sealed to the account (follows snapshot sealing 3d-3, still behind its flag); the cold resume from the volume (step 5, so it still starts from a fresh workspace).

### Step 5 as built

- **The volume.** One sparse ext4 file per session under `/srv/fc/sandbox-volumes/<account>/<session>.ext4` (XFS, reflinks), labelled `gregale-ws`, its root owned by the session's user. vmmd attaches it as drive 2, bound into the jail and never copied, at a cold boot and at a capture's restore; guest-init mounts it at `/workspace` (a volume that does not mount fails the boot). A session with a volume never restores a warm snapshot, which has no such drive.
- **Backups.** A backup clones the volume (a reflink on XFS, so it is point-in-time and the VM keeps running: crash-consistent while running, exact at a pause) and reads only the blocks whose extents differ from the clone the node's last backup read; a first backup reads only data, not holes. It stores 4 MiB chunks, zstd-compressed, once per account under their SHA-256 (`sandbox-volumes/<account>/chunks/<sha256>`), and one manifest per backup listing them (`sandbox-volumes/<account>/<session>/backups/<id>.json`). A manifest is complete on its own: a restore reads one, never a chain, and checks every chunk's hash. Forks will share the chunks they have in common.
- **When.** vmmd backs a running volume up every `SandboxVolumeBackupSec` and reports each (`volume_backup`); a pause clones the volume as the capture left it and uploads that in the background with the capture, naming it in `paused`. A clone a restart left is uploaded at start.
- **Which copy.** agentd records the node holding the newest copy and the newest backup. vmmd keeps the node's copy when it is that backup or newer, and otherwise restores the backup before the boot; a volume it cannot prepare leaves the session paused to try again, never booted without its files. A session that moved deletes the copy on the node it left.
- **Durability.** A capture of a session with a volume is `remote` only once its volume backup's manifest is in the shared store too; until then it resumes only on its node. A cold resume on another node therefore gets the workspace as the newest backup left it (losing at most `SandboxVolumeBackupSec` of writes when the node was lost).
- **Grow.** `PATCH {volume_gb}` grows a paused session's volume within the plan and never shrinks it; vmmd grows the file and the filesystem offline (`e2fsck`, `resize2fs`) before the resume boots. A capture's memory holds the mounted filesystem at its old size, so a grow uses the session's capture up and the session resumes cold (`resumed_cold`): files kept, processes started again (review fix, 2026-11-26).
- **Cleanup.** The sweep deletes the manifests of backups a newer one superseded (none a live capture names), and when a session ends its node copy (through schedd's `DeleteSandboxVolume`), every manifest and its row.
- **Metrics.** `sandbox_volume_backup_seconds` and `sandbox_volume_backup_bytes` (running backups); vmmd's operations `SandboxVolumeBackup` and `SandboxVolumeRestore`.
- **Not yet:** collecting chunks no manifest refers to (they stay in the store); backups sealed to the account (with captures, after 3d-3); a `sync` in the guest before a running backup (a backup is crash-consistent, as after a power cut).

### Step 6 as built

- **Request.** `POST /v1/sandboxes/{id}/fork {count, kind?, labels?}` queues `count` sessions `forked_from` the source with its machine, network, image, egress policy and volume size; `kind` defaults to `ephemeral`, labels to the source's. An ephemeral fork needs the source's RAM, vCPUs and volume within the ephemeral shapes. `SandboxForkMax`, the queued cap and the persistent cap apply to the whole request under the account's lock. The route is dark behind `FAAS_SANDBOX_PERSISTENT`.
- **Which capture.** A paused source's forks restore its pause capture. For a running or frozen source apid sets `fork_requested_at`; agentd sees it at the next lease renewal and sends `fork`. vmmd takes a live capture (the warm-snapshot path: the VM pauses only while its memory is written, node-first) and clones the volume while the guest is paused, so the two agree. It answers `forked`, and agentd records a capture of purpose `fork` and hands it to every fork still waiting. A pause and a fork never run together; a refused or unanswered fork is retried.
- **Start.** A fork is claimable only once the shared store holds its whole capture and the volume backup (`uploaded_at`, set by the agentd sweep). It restores on any node as `Resume{fork}` with the source's vCPUs, and its volume restores from the source's backup (`volume_source_sandbox_id`). An ephemeral fork's volume is still recorded, so it is deleted when the fork ends.
- **Lifetime of what forks share.** A capture, the backup it names and the source's volume row stay while a queued or starting fork names the capture, even after the source resumed or ended. Once none does, a fork capture is consumed (after a minute's grace) and the capture sweep deletes its objects. A fork whose source ended before it was captured fails with `fork_source_ended`.
- **Measured** (EC2 nested KVM, 512 MB guest with a 2 GB volume): live fork capture 7.6 s node-first; two forks restored in 318 ms and 279 ms with distinct host IPs, the source's memory and files, and writes isolated both ways.
- **Not yet (fork):** ~~the reflink fast path~~ (amendment "same-node forks"); ~~the `GREGALE_FORKED_FROM` marker and `SIGUSR2` reseed hook~~ and ~~`sandbox_fork_seconds` and `sandbox_forks_total`~~ (amendment "fork marker and metrics"); clients (step 10).

### Step 7 as built (evacuation)

- **One request, two senders.** A session is evacuated when schedd sees its agent-pool node leave (`draining`, `force_draining`, `maintenance`, `retired`; checked every 5 s from the node registry) or when its vmmd stops. Both send `evacuate{reason}` on the session's stream (`node_draining`, `vmmd_stopping`), at most once per sender.
- **What agentd does.** A persistent session pauses at the next tick (after a fork in flight, never refused for being early), and its pause asks for its resume at once (`resume_requested_at`). An ephemeral session ends with `node_evacuated`.
- **Where it resumes.** A capture only its node holds (durability `node`) keeps `require_node` while that node is draining: the resume waits, refused for capacity and tried again, until the upload makes it `remote` and it restores on another node. After a vmmd restart the node is still active, so the resume restores the node's copy there once vmmd serves again.
- **vmmd stop.** Before it stops serving, vmmd sends `evacuate` to every live session and waits up to 75 s (inside systemd's default stop timeout) for their streams to end; sessions not moved by then are destroyed as before.
- **Reattach (schedd and agentd restarts).** schedd starts a persistent session's VM `detachable`. When its stream ends without a close or a pause, vmmd keeps the VM running and its data-plane calls served for `SandboxReattachGraceSec` (120 s), then destroys it as before. agentd does not fail a running persistent session whose stream dropped (`Unavailable`): its lease lapses, the sweep leaves it alone within the grace, and the next claim on any replica takes it over (`ClaimDetachedSandbox`, before queued sessions). The claim's start carries `reattach{node, instance}`; schedd books the VM on that node again without placement (running it even if the ledger has no room, since the VM is resident), and vmmd hands the running VM to the new stream (`booted{reattached}`). A VM that did not outlive its stream answers `NotFound` and the session fails as `vm_lost`. Actions in flight when the stream dropped are ended.
- **Lease loss with a volume.** A running persistent session with a volume whose lease lapsed waits out the same grace before it goes back to paused for a cold resume (§4): within it, a replica may still reattach.
- **Not yet:** a node drain does not wait for its sessions (drain completion counts app instances only); adoption of session VMs across a vmmd restart (a stopping vmmd evacuates instead); an ephemeral session still ends with its stream.

### Step 8 as built

- **Declared nodes.** Until `compute_nodes` carries the agents role, an agent-pool node declares large shapes by being listed in schedd's `FAAS_SCHEDD_SANDBOX_LARGE_NODES` (IDs or names, a subset of `FAAS_SCHEDD_AGENT_RUN_NODES`).
- **Large.** A session above `SandboxLargeShapeRAMMB` (4,096 MB, the largest ephemeral shape) or `SandboxLargeShapeVCPU` (2) is placed only on those nodes: a new session, a resume, a fork and a drain's move alike. With none declared it is refused for capacity and queues, failing after `SandboxQueueMaxSec` as any session no node can place. A reattach goes to the node it runs on.
- **Unchanged:** invariant 2 per node, measured admission, and the plan limits (`SandboxPersistentRAMMBMax`, `SandboxPersistentVCPUMax`).
- **Not yet (large shapes):** packing large sessions before small ones (small sessions may still land on a large node); a per-node maximum shape; the manifest and Ansible key for the list.

### Step 9 as built (dark: metered, not billed)

- **Owner's choice (2026-11-23): build it dark.** Stored bytes are metered and shown; nothing prices or bills them until the GB-month price is set (open question 1).
- **What counts.** A session's stored bytes are its live pause capture (memory and vmstate bytes as captured; the compressed upload size is not recorded yet, so this over-counts what the store holds) plus its newest volume backup's data (data chunks × 4 MiB, before compression). Fork captures are not counted to the source; a fork pays for itself once it runs.
- **Metering.** agentd's sweep writes `sandbox_storage_hours`: per account and UTC hour, its paused sessions and their capture and volume bytes. The first sweep of an hour wins; an hour without paused sessions has no row. Running sessions are not metered here (their compute is).
- **Showing it.** A session's view has `stored_bytes`. `GET /v1/sandbox-storage?from=&to=` lists an account's metered hours (the current UTC month by default, at most 31 days), their GB-hours, the plan's `SandboxPausedBytesFreePerAccount` in GB, and `billable: false`.
- **Not yet:** the price, the statement line and meterd's export to billing; the compressed capture size; deduplication across a fork tree.

### Amendment: prompt requests and fork starts

agentd learned of a pause, close or fork request at its next lease renewal (a third of the lease TTL, 10 s by default). A fork stayed unclaimable until the sweep (`SweepInterval`, 10 s) found its capture in the shared store. So a fork of a running session could wait about 20 s on polling around 1–3 s of VM work.

- **Nudge.** After recording a pause, close or fork request, apid sends `pg_notify('sandbox_session_request', <session id>)`; a fork request sends it inside its transaction, so it goes out at commit. The agentd replica holding the session renews its lease at once, and its 1 s session tick acts on the request. The row stays the request: a lost notification waits for the next renewal, as before. A resume, and every fork start, is a claim, which agentd polls every second.
- **Upload watch.** The replica that records a fork capture checks the shared store for its objects every second, for up to five minutes, and marks the capture uploaded as soon as the store holds them all. The sweep stays the fallback.

### Amendment: same-node forks

A fork of a running session still waited for its capture's upload to the shared store, seconds for a large memory file, although the node that took the capture holds it already. Owner decision 2026-11-23: start on the capture's node first.

- **Claim.** A fork is claimable as soon as its capture exists. Until the capture is uploaded, agentd starts it with `RequireNode` on the capture's node, the way a resume restores a capture that is not uploaded yet (§2). schedd accepts `Fork` with `RequireNode` and refuses a pinned fork whose node is not active with a capacity error.
- **Refusal.** A pinned fork that does not become ready (no room on the node, the node gone, or any failure before ready) goes back to the queue with `sandbox_sessions.fork_local_refused_at` set. It is then claimable only once the capture is uploaded, and starts on any node, as before this amendment. A fork is never failed for being refused on the capture's node.
- **Volume.** vmmd clones the fork's volume from the node's own clone of the source's fork backup: the pending clone while it uploads, the base once it has. A node without either reads the backup from the store. The fork has no backup base of its own, so its first backup is full.
- **Migration.** `fork_local_refused_at timestamptz` is an expand-only column. A replica of the previous release ignores it and waits for the upload as before; a previous schedd rejects `Fork` with `RequireNode` as invalid, which agentd treats as a refusal.

### Amendment: fork marker and metrics

- **Marker.** agentd sends a fork's source in `SandboxRunResume.forked_from`. schedd passes it to vmmd as `RunSandboxStart.forked_from`, which vmmd accepts only with `session_capture` and only when it names another session. Once the fork's guest answers, vmmd sends it the host-only `forked` op. From then on, guest-init sets `GREGALE_FORKED_FROM` in every command's environment, over the session's env and the command's own. A guest-init from before this amendment refuses the op; vmmd logs it and the fork runs on without the marker.
- **Reseed hook.** `POST …/exec` takes `reseed_on_fork` for a background command. A foreground command ends with its call, so it never runs on in a fork, and the flag is refused there. On `forked`, guest-init sends `SIGUSR2` to the process group of each running command that asked. Commands that did not ask are left alone.
- **Metrics.** `sandbox_fork_seconds{count_bucket=1|2-4|5+}` is the time from agentd asking for a running session's fork capture to vmmd recording it. `sandbox_forks_total{outcome}` counts fork starts: `node` (restored on the capture's node), `remote` (from the shared store), `cold`, `refused_node` (the capture's node refused it and it waits for the upload) and `orphaned` (its source ended before the capture). Both are on the agent runs dashboard.

## Open questions

1. **Prices:** GB-month for stored bytes and the free allowance, alongside `k` and the 256 MB floor.
2. **Fork tree storage:** dedup of shared capture pages across forks beyond the shared base image. Measure first.
3. **Diff snapshot chain length** before a background merge; measure restore time against chain length on the reference node.
4. **Cross-CPU restores:** a capture is pinned to `cpu_template`/`cpu_model` (ADR-20261024225612). On a pool with mixed CPUs, does resume prefer a matching node or fall back to cold? Proposed: prefer, then cold.
5. **GPU** stays phase 3 (ADR-20261027090014 §5).
