# ADR-20261027090015 · Agent memory tiering: warm preset snapshots, compress-on-wait, and a measured reservation for agent VMs

- **Status:** proposed. The native-host latency check (§6) is owed before compress-on-wait is turned on.
- **Date:** 2026-10-19
- **Amended by ADR-20261027090019** (proposed), §1: preset snapshots carry two empty context drive slots, filled at restore.
- **Amends (when accepted), for agent VMs on agent nodes only:**
  - ADR-909, proposal 1 (charge) and proposal 2 (the overcommit bound), and with them spec §6.2 invariant 2;
  - ADR-20261027090010 §2 (preset snapshots);
  - ADR-20261027090012 §4 (memory stays resident while paused);
  - ADR-20261027090014 §2 and §3 (invariant 2 wording and per-node estimates).
- **Builds on:**
  - ADR-906 decision 6, compressed swap with writeback disabled;
  - ADR-1151, pause tiers;
  - ADR-171 and ADR-481, the snapshot contract and post-restore reseed;
  - ADR-005, cold boot always works.
- **Related:** ADR-20261027090011 (average used RAM billing), ADR-20261027090013 (sandbox sessions), ADR-203 (role-aware sizing).
- **Evidence:** `scripts/experiments/agent-runs/MEMORY_EXPERIMENT.md` on branch `proto/agent-runs` (`93aa01710c`).
- **Decision:**
  1. **Warm preset snapshots.** A preset snapshot is captured after a platform warm-up run, not just after the harness loads. The warm-up runs one scripted session against a loopback stub provider, then deletes its home directory before capture. Runs restore from it by default. The shared snapshot pages are counted once per node, as preset residency.
  2. **Compress-on-wait.** A quiet-paused agent run whose wait has lasted `AgentWaitCompressAfterMillis` can be compressed. vmmd opens memory-only swap for it and reclaims its memory into zswap. It does this only while the node's measured memory is above `AgentWaitCompressWatermarkPercent` of the ceiling. The switch is `agent_wait_compress`, default off, and it stays off until the native-host check passes.
  3. **A measured reservation for agent VMs.**
     - What an agent VM counts against admission comes from its agent version's measured size history, not its configured RAM.
     - That applies to the ADR-909 charge and to the reservation in the 2× overcommit bound.
     - `memory.max = ram_mb + 8 MB` is unchanged, so no VM can exceed its configured RAM.
- **Why:**
  - **The 2× reservation cap is now the limit, not RAM.** Under ADR-909, Σ(ram_mb + 8) ≤ 2 × 47,600 MB stops a node at 46 agent VMs of 2048 MB. Their measured memory would allow about 850–1,400 Codex or Claude Code runs.
  - **Short runs never earn a measured charge.** ADR-909 charges a new instance its full plan until it has 10 minutes of history, and most agent runs end before then. So Σ charges ≤ ceiling would stop a node at about 23 runs of 2048 MB.
  - **A warm restore is 3–5× smaller than a cold boot.** A cold boot reads the harness files into guest page cache, which the host holds as private anonymous memory. A restore maps the same pages from the shared, clean snapshot file. Codex drops from 335 to 62 MB of private memory and Claude Code from 332 to 102 MB, and the first model call comes 6–8 s sooner.
  - **A paused run keeps its memory.** ADR-20261027090012 frees CPU, not memory. After a reclaim into zswap, a waiting Codex run holds 21 MB and Claude Code 36 MB with lz4. With zstd they hold 14 MB and 25 MB.
  - **The owner's goal (2026-10-18):** keep Firecracker, and change agent economics on RAM-limited nodes such as the EX44.
- **Consequences:**
  - **Capacity.** A 64 GB agent node holds about 500 concurrent Codex or Claude Code runs. ADR-20261027090014 estimated 50–90. Active vCPU and the reservation bound now limit it, not RAM (§5).
  - **Admission and eviction.** Admission relies on statistical multiplexing: not every run is active at once. A thaw headroom and the eviction path from ADR-909 cover the rest.
  - **CPU.** Compression costs host CPU: 0.3–1.6 s per wait with lz4 and 0.7–2.5 s with zstd, measured on a nested host. Compression is therefore limited to long waits under memory pressure.
  - **Latency.** The first turn after a compressed wait refaults its working set (16–130 MB here). On the nested host that turn was 0.4–1 s slower. The native check (§6) decides whether this is acceptable.
  - **Configuration.** Agent nodes must run the zswap role (`faas_zswap_enabled: true`) with a larger pool than compute nodes.
  - **Invariant 2 changes wording for agent VMs.** Its property tests are amended, not deleted. CLAUDE.md is updated when this is accepted.
  - **Billing is unchanged.** Average private memory under ADR-20261027090011 already excludes shared snapshot pages, and paused seconds are unbilled under ADR-20261027090012. The cost per GB-h falls, which the financial model's `k` should reflect.
- **Rejected alternatives:**
  - **KSM:** cross-tenant page merging is a side channel, and scanning costs CPU continuously.
  - **Swap or zswap writeback to NVMe:** tenant memory would reach a disk. The security rules and ADR-906 decision 6 forbid that.
  - **Raising the overcommit factor alone and keeping `ram_mb` reservations:** reaching the measured density would need a factor of about 20–30. The bound would stop meaning anything, and it would ignore what each agent version actually uses.
  - **Smaller configured RAM per run:** tool work (installs, builds, tests) needs burst headroom, and OOM reruns cost more than the memory saved.
  - **Compressing every wait as soon as it pauses:** it spends reclaim CPU and refaults on short waits that never needed the memory.
  - **Keeping a loaded harness process in the snapshot:** CLI harnesses take the prompt as a start-up argument. Feeding a prompt to a restored process would mean changing harnesses, which D2 rules out.
  - **Balloon inflation while waiting:** it needs guest cooperation and cannot reach the harness's live heap. Free page reporting (ADR-907) already returns freed pages.

## Context

The 2026-10-18 experiment ran on `gregale-internal-test-1`:

- a nested-KVM GCP VM with 2 vCPU and 16 GB, shared and loaded;
- Firecracker 1.7.0, so no free page reporting;
- three presets restored and cold-booted as real Firecracker guests.

Each guest ran a scripted session against an in-guest fake provider that held every response until it was released. Memory numbers are valid under nested virtualization, because the L1 kernel measures cgroups, page sharing and zswap. Latencies are not acceptance numbers. The sessions were light (cat, rg, ls), so the numbers are a floor for each harness. Tool processes add to them in real runs.

| Preset | Private memory, warm restore (avg / max) | Cold boot (avg) | After pause + reclaim, lz4 / zstd | Shared across 4 restores | Harness CPU while waiting (ms per 2 s, mean / max) |
|---|---|---|---|---|---|
| Codex | 62 / 77 MB | 335 MB | 21 / 14 MB | 57% | 7.6 / 55 |
| Claude Code | 102 / 120 MB | 332 MB | 36 / 25 MB | 45% | 37 / 164 |
| OpenCode | 368 / 481 MB | 581 MB | 131 / 282 MB¹ | 20% | 105 / 701 |

¹ With zstd, the reclaim hit the 2 s budget and moved only half of OpenCode's memory.

Other measured points:

- **Compression:** lz4 compresses 3.4–3.7×, and zstd 5.2–5.5× but 1.5–3× slower.
- **Thaw:** 1–37 ms.
- **Release burst:** four VMs released at once rose straight to their post-release level, with no spike.
- **Shared snapshot pages:** 170–263 MB per preset, charged to the cgroup that first faulted them. A restored VM's `memory.current` does not include them.
- **Growth:** 1.7 MB per turn (Codex) and 3 MB per turn (Claude Code) over 20 turns of 50 KB tool output.

## Decision

### 1. Warm preset snapshots (amends ADR-20261027090010 §2)

**Capture.** The preset snapshot builder boots the preset on drive0 with a platform layer and no tenant identity, input or secret. Inside the guest it:
1. runs the preset's harness once against a loopback stub provider, with a platform-owned script of two turns: one shell call, then a final answer;
2. uses a throwaway `HOME` and working directory, and deletes both after the harness exits;
3. stops the stub.

The snapshot is then taken with the harness's files in guest page cache and no harness process running.

**Contract checks before publication** (ADR-171):
- `HOME` and the work directory are empty;
- no harness installation identifiers or session files remain (Claude Code `~/.claude*`, `$CODEX_HOME`, OpenCode state);
- no outbound connections were made other than the loopback stub.

Two restores of the snapshot must differ in IP, netns, jail uid and RNG stream (invariant 5, ADR-481).

**Default path.** Runs restore from the warm preset snapshot. A missing or stale snapshot falls back to a cold boot of the same image (ADR-005). The fallback is charged as a new instance (§3) until it has history.

**Preset residency.** vmmd reports the resident page cache of each preset snapshot's memory file and of drive0 (from `mincore`) as `preset_residency_mb`. The node's admission ceiling subtracts it once per preset in use, as ADR-203 subtracts base residency. The snapshot pages are shared by every run restored from them, so they are not counted per run.

**Preset choice.** A preset whose warm restore shares less than 30% of its pages, or reaches its first model call no sooner than a cold boot, gains nothing from this. That is OpenCode on the evidence so far. Such a preset still restores, but its residency is not worth pinning on nodes that do not run it.

### 2. Compress-on-wait (amends ADR-20261027090012 §4)

**When.** vmmd compresses an agent run VM only when all of these hold:
- it is quiet-paused under ADR-20261027090012;
- it has stayed paused for `AgentWaitCompressAfterMillis`;
- the node's measured memory is above `AgentWaitCompressWatermarkPercent` of the admission ceiling.

The VMs that have waited longest are compressed first.

**How.** The machinery is ADR-906 decision 6's (`pkg/fcvm/frozen_swap.go`):
- vmmd opens `memory.swap.max` on the VM scope only if `frozenSwapReady` holds: zswap on, a swap device present, and `memory.zswap.writeback=0` on an ancestor;
- it runs `memory.reclaim` within the existing budget;
- it closes `memory.swap.max` before the guest resumes.

Tenant memory never reaches a disk. Pages return by refault as the guest touches them.

**Resume.** Resuming is ADR-20261027090012 §3, unchanged: relay bytes or any vmmd operation resume the VM first. Compression never delays a resume.

**Reporting.** vmmd reports each instance's wait state (`running`, `paused`, `compressed`) alongside the memory sample that schedd already consumes.

**Billing.** Unchanged. Compressed seconds are paused seconds, and are unbilled.

**Compressor.** Agent nodes use zstd, with lz4 as the fallback. zstd stores about 1.5× more per pool byte, and compression runs only on long waits. The native check (§6) confirms or reverses this.

### 3. Measured charge and reservation for agent VMs (amends ADR-909 proposals 1–2 and invariant 2)

For an agent VM on an agent node, the terms come from `agent_size_history` (ADR-20261027090010), per agent version and input class. That table gains a `p95_private_peak_mb` column: anon + shmem, the ADR-20261027090011 meter. A version with fewer than `AgentSizeHistoryMinRuns` runs uses its `ram_mb` instead.

**Charge** (the ADR-909 term, Σ charges ≤ ceiling × (1 − `AgentThawHeadroomPercent`)):

| VM state | Charge |
|---|---|
| running or paused | max(`memory.current` − reclaimable file pages, `p95_private_peak_mb`, `AgentChargeFloorMB`) + 8 MB |
| compressed | `memory.current` (the zswap pool is included) + 8 MB |

The charge is capped at `ram_mb` + 8 MB. A run is charged from its first second by its version's history, not by its plan.

**Thaw headroom.** A compressed run grows back when its response arrives: 16–130 MB in the 5 s after a thaw, as measured. The node covers this with a single headroom (`AgentThawHeadroomPercent` of the ceiling), not with a per-VM allowance.

- **Independent wake-ups.** With N runs each active for a share `a` of the time, the number active at once varies by √(N·a·(1−a)). For 600 Claude Code runs at a = 0.3, that is about 11 runs. Three standard deviations times the difference between the running and compressed charge (128 − 44 MB) is about 2.9 GB, inside a 10% headroom of 4.8 GB.
- **Correlated wake-ups** (for example, many responses arriving together after a provider outage ends) are left to the eviction order below.

**Reservation** (the ADR-909 overcommit term):

> Σ(reservation + 8) ≤ `MeasuredAdmissionMaxOvercommitPercent` × ceiling

Here reservation = min(`ram_mb`, max(`AgentReservationFloorMB`, `AgentReservationPeakFactorPercent` × `p95_private_peak_mb`)).

**Invariant 2 for agent nodes** then reads:
- measured charge ≤ the ceiling minus the thaw headroom;
- Σ(reservation + 8) ≤ 2 × the ceiling;
- eviction applies above the ceiling.

App VMs keep `ram_mb + 8` everywhere.

**Eviction order** on an agent node, when MemAvailable falls below the ADR-909 floor or memory PSI rises:
1. stop admitting;
2. compress waiting runs that are not compressed yet, ignoring the watermark;
3. evict the newest-started running agent runs, which loses the least work.

An evicted run is rerun (ADR-20261027090010 §8), and its seconds before eviction are not billed.

### 4. Agent nodes

- **zswap.** The zswap role runs on every `agents` and `compute+agents` node with `faas_zswap_compressor: zstd` and `faas_zswap_max_pool_percent: 40`. At 25%, the pool of a 64 GB node fills at about 900 compressed Codex runs.
- **Firecracker.** Agent nodes run the fleet Firecracker with free page reporting (ADR-907).

### 5. Capacity

Per node, measured memory ceiling 47,600 MB, with `a` the share of wall time a run is active. The limits are the light-session floors measured above.

| Preset | Old bound (`ram_mb` 2048, 2×) | Charge rule (§3), lz4, a = 0.3 | Reservation bound (§3) | Active vCPU, 1 vCPU per active run, a = 0.3 |
|---|---|---|---|---|
| Codex | 46 | ~935 | 700 (floor 128) | ~530 |
| Claude Code | 46 | ~620 | 506 | ~530 |
| OpenCode | 46 | ~87 (never paused, so never compressed) | 130 | ~160 |

How the columns are computed:

- **Charge rule:** (1 − 10%) × 47,600 divided by a·(p95 peak + 8) + (1 − a)·(compressed + 8).
- **Reservation bound:** 2 × 47,600 divided by (reservation + 8).
- **Active vCPU:** `VCPUSlots` = 160 divided by `a`.

A node holds the lowest of these: about 500 Codex or Claude Code runs per 64 GB node, or about 90 OpenCode runs, against ADR-20261027090014's estimate of 50–90. At a = 0.5, CPU binds at about 320 and Claude Code's charge rule at about 500.

### 6. Native-host check (owed)

The check runs on a native x86_64 KVM host with the fleet Firecracker, such as fsn-4 or an EX44-class node. It uses the experiment's tooling, without in-guest status polling. It must pass before `agent_wait_compress` is turned on for any customer, and its results replace the proposed values in the limits table.

| Check | Pass |
|---|---|
| Added latency from release to the harness's next request, after an uncompressed pause (20 waits per preset at 10 s and 60 s) | ≤ 50 ms p95 |
| The same after a compressed wait, lz4 and zstd | ≤ 250 ms p95. If not met, the watermark rises to 80% and compression becomes a pressure valve only |
| Reclaim CPU per compression, lz4 vs zstd | Sets the compressor and `AgentWaitCompressAfterMillis` |
| Harness CPU while waiting, with no in-guest polling | Sets `AgentWaitPauseCPUMaxMillis` (ADR-20261027090012) for Claude Code's spikes |
| Free page reporting plus a guest cache drop before compression | Included if it returns ≥ 20% more memory |
| Warm restore to first model call | ≤ 1 s to a ready VM (ADR-20261027090010), and first model call ≤ 3.5 s for Codex and Claude Code |
| Density soak: 100 runs on 64 GB at a ≈ 0.3 for 30 minutes | No eviction; memory PSI some avg10 < 10%; measured charge below the ceiling minus the headroom |

## Limits (`pkg/api/limits.go`, proposed)

| Name | Value |
|---|---|
| `AgentChargeFloorMB` | 64 |
| `AgentReservationFloorMB` | 128 |
| `AgentReservationPeakFactorPercent` | 150 |
| `AgentSizeHistoryMinRuns` | 20 |
| `AgentThawHeadroomPercent` | 10 |
| `AgentWaitCompressAfterMillis` | 10,000 |
| `AgentWaitCompressWatermarkPercent` | 60 |
| `MeasuredAdmissionMaxOvercommitPercent` | 200 (existing, unchanged) |

## Metrics (§12 additions)

- `vmmd_agent_wait_compress_total{outcome}`
- `vmmd_agent_wait_compress_seconds`
- `vmmd_agent_wait_compressed_instances`
- `vmmd_agent_thaw_refault_bytes`
- `vmmd_preset_residency_mb{preset}`
- `sched_agent_charge_mb{state}`
- `sched_agent_reservation_mb`
- `sched_agent_evictions_total{reason}`

## Rollout

All steps stay on local branches until the owner asks for PRs. Each sits behind the agent switches (the internal `agents` capability, default nobody). VM-lifecycle steps run `make test-metal` and `make leakcheck`.

1. This ADR, accepted together with ADR-20261027090010, ADR-20261027090011 and ADR-20261027090012.
2. **Warm preset snapshot builder** (ADR-20261027090010 rollout step 9), with metal tests:
   - a restored run's private memory stays under its history;
   - `HOME` is empty and there are no installation identifiers;
   - two restores differ in identity and RNG.
3. **`preset_residency_mb`** in vmmd and in the agent node's ceiling.
4. **Agent charge and reservation in schedd** behind `agent_measured_charge`. Property tests:
   - Σ charges ≤ ceiling × 0.9;
   - Σ(reservation + 8) ≤ 2 × ceiling;
   - reservation ≤ `ram_mb`;
   - `memory.max = ram_mb + 8`.
5. **vmmd compress-on-wait** behind `agent_wait_compress` (default off). Metal tests:
   - a held relay response pauses and compresses the VM, and the first byte resumes it;
   - no swap is opened without writeback disabled;
   - 1,000 cycles leave no leaks.
6. **The native-host check (§6),** then the limits are set from it.
7. **Turn on** with ADR-20261027090011 billing at milestone B. Amend ADR-20261027090014 §3 with measured capacity.

## Open questions

1. Whether OpenCode stays a default preset. It is 4–6× heavier, gains nothing from a warm restore, and is not idle while waiting.
2. Whether sandbox sessions (ADR-20261027090013) adopt compress-on-wait between tool calls with the same rules. Their idle gaps are longer and their working sets larger.
3. Whether per-account snapshots of seller images get the same warm-up. It needs a seller-declared warm-up command.
4. How fast `agent_size_history` must react when a new agent version is heavier. Today it relies on `AgentSizeHistoryMinRuns` runs at `ram_mb`.
