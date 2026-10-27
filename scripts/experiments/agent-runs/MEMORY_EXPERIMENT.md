# Keyless agent memory experiment

Status: run on gregale-internal-test-1, 2026-10-18 21:09–22:04 UTC. Cells
A–E passed for all three presets. Cell F was not measured. No model keys
were used anywhere. The host was cleaned up afterwards, as described in
"Safety and cleanup". The plan that was followed starts at "Why the model
must be fake".

The memory-tiering ADR waits for these numbers. The question is how many
agent runs one node can hold. Before the run, the density estimate assumed
three inputs:

| Input | Assumed | Measured (warm restore, lz4) |
|---|---|---|
| Private memory of an active run | 500 MB | Codex 62, Claude Code 102, OpenCode 368 MB average |
| Memory of a run waiting on the model | 1/6 of active | 21 / 36 / 131 MB after pause and reclaim (34–36% of active); zstd gives 14 / 25 MB (23–25%) |
| Saving from a warm preset snapshot | 150 MB | 230–270 MB less private memory than a cold boot (Codex, Claude Code); 45–57% of pages shared across 4 restores |

## Results

Setup:

- **Host:** gregale-internal-test-1, a GCP VM with nested KVM. 2 vCPU, 16 GB, shared and loaded (load 7).
- **Firecracker:** 1.7.0, which has no free page reporting.
- **Guest:** 2048 MB, 2 vCPU, plan `scale`.
- **Images:** the base is the agent image as drive0, about 1.8 GB. The per-run layer is 64 MB.
- **Swap:** zswap in front of zram, with `memory.zswap.writeback=0` on `faas-tenant.slice`.
- **Repeats:** cell A ran 3 times. Cells B and E ran once per compressor.
- **Raw data:** the samples are kept off the repo.
- **Summaries:** `memory/summarize_memory.py results/*.jsonl`.

### A. Active profile (MB, s; mean of 3)

| Preset | Start | Private avg | Private max | Charge peak | Snapshot pages mapped | To first model call | Harness time excl. holds |
|---|---|---|---|---|---|---|---|
| Codex | cold | 335 | 409 | 640 | 0 | 11.45 | 13.9 |
| Codex | warm | **62** | 77 | 187 | 263 | **3.32** | 4.7 |
| Claude Code | cold | 332 | 395 | 564 | 0 | 8.74 | 10.0 |
| Claude Code | warm | **102** | 120 | 132 | 196 | **2.78** | 4.2 |
| OpenCode | cold | 581 | 705 | 711 | 0 | 13.46 | 15.0 |
| OpenCode | warm | 368 | 481 | 499 | 170 | 13.25 | 14.6 |

Baseline VM (probe running, no harness): 6 MB private.

A cold boot pulls every harness file into guest page cache. On the host, that page cache is private anonymous memory. A restore maps the same pages from the snapshot file, which is shared and clean, so private memory drops by 230–270 MB per run. Start-up drops by 6–8 s.

OpenCode is the exception. It still takes 13 s to its first model call after a restore, which looks like a start-up timeout when it has no egress. OpenCode is also Bun-heavy: about 500 MB of harness RSS.

### B. Waiting on the model: one 60 s hold on turn 2 (MB, ms)

| Preset | Mode | Charge while waiting | After freeze | Swapped | Pool | Ratio | Reclaim | Thaw | Release → next request (s) |
|---|---|---|---|---|---|---|---|---|---|
| Codex | none | 79–98 | — | — | — | — | — | — | 0.42 (one 3.35 outlier on the loaded host) |
| Codex | pause | 93–103 | 73–98 | 0 | 0 | — | 0 | 4–7 | 0.19–0.34 |
| Codex | reclaim lz4 | 54 | **21** | 67 | 18 | 3.5× | 291 | 9 | 0.25 |
| Codex | reclaim zstd | 44 | **14** | 69 | 12 | 5.5× | 678 | 9 | 1.09 |
| Claude Code | none | 136–144 | — | — | — | — | — | — | 0.25–0.33 |
| Claude Code | pause | 137–139 | 132 | 0 | 0 | — | 0 | 1–4 | 0.42–1.11 |
| Claude Code | reclaim lz4 | 86 | **36** | 119 | 34 | 3.4× | 366 | 12 | 0.66 |
| Claude Code | reclaim zstd | 81 | **25** | 119 | 22 | 5.2× | 1223 | 14 | 0.79 |
| OpenCode | none | 484–509 | — | — | — | — | — | — | 0.21–0.67 |
| OpenCode | pause | 477–511 | 476–506 | 0 | 0 | — | 0 | 7–8 | 0.23–0.64 |
| OpenCode | reclaim lz4 | 213 | **131** | 481 | 127 | 3.7× | 1643 | 37 | 1.34 |
| OpenCode | reclaim zstd | 345 | 282 | 236 | 43 | 5.4× | 2540 | 33 | 1.22 |

How to read these rows:

- A pause alone frees nothing. The memory goes only when the pause is followed by a reclaim into compressed swap.
- zstd stores about 1.5× more per pool byte than lz4, but it reclaims 1.5–3× slower. For OpenCode's 480 MB it ran into the reclaim time budget and moved only half the pages.
- Pages read back in the 5 s after a thaw: 4k (Codex), 15–18k (Claude Code), 18–34k (OpenCode). This is why the first turn after a compressed wait runs 0.4–1 s slower than an uncompressed one on this host.

### C. CPU while waiting, no pause (ms per 2 s; ADR-20261027090012 pauses under 50)

| Preset | Harness tree (guest) mean / max | VM cgroup mean / max |
|---|---|---|
| Codex | **7.6** / 55 | 79 / 184 |
| Claude Code | 37 / 164 | 104 / 263 |
| OpenCode | 105 / 701 | 197 / 901 |

The VM cgroup figure includes 35–53 ms per 2 s that an idle VM spends answering the test's once-a-second status polls (measured on the baseline VMs). The harness tree column is the clean number.

- Codex is idle while it waits.
- Claude Code averages under the ADR-20261027090012 threshold but spikes above it.
- OpenCode is not idle.

The threshold needs calibrating on a native host without in-guest polling.

### D. Growth over 20 turns of 50 KB tool output (private MB)

| Preset | Call 1 | Call 10 | Call 20 |
|---|---|---|---|
| Codex | 59 | 81 | 92 |
| Claude Code | 111 | 150 | 172 |
| OpenCode | 496 | 499 | 550 |

That is roughly 1.7 MB per turn for Codex and 3 MB per turn for Claude Code.

### E. Four restores of one snapshot (MB, sums over the 4 VMs)

| Preset | Phase | Σ charge | Σ private | Σ Rss | Σ Pss | Shared (1 − Pss/Rss) |
|---|---|---|---|---|---|---|
| Codex | all waiting | 356 | 272 | 1185 | 510 | 57% |
| Codex | all compressed (lz4) | 82 | 0 | 910 | 236 | 74% |
| Claude Code | all waiting | 480 | 468 | 1215 | 663 | 45% |
| Claude Code | all compressed (lz4) | 143 | 0 | 747 | 195 | 74% |
| OpenCode | all waiting | 1928 | 1883 | 2586 | 2068 | 20% |
| OpenCode | all compressed (lz4) | 727 | 286 | 988 | 470 | 52% |

How to read this table:

- **Shared pages per snapshot:** the snapshot's shared pages come to about 200–260 MB per preset. They are a fixed cost per node, not a cost per run.
- **Accounting:** those pages are charged to whichever cgroup faulted them first, which here was the process that parked the VM. So a restored VM's `memory.current` understates the host's use by that fixed amount.
- **Burst:** releasing all four at once added no spike. Σ charge rose straight to its post-release level (Codex 240 MB, Claude Code 413 MB, OpenCode 1119 MB).
- **Host-level used Δ:** too noisy on this shared host to use.

### Density per node (measured memory ceiling 47,600 MB)

`N = 47,600 / (a·M_active + (1−a)·M_wait)`, with:

- `M_active` = warm private average (cell A);
- `M_wait` = the charge after reclaim (cell B);
- the shared snapshot pages left out (about 0.75 GB per node for three presets).

| Preset | No compression (a = 0.3) | lz4, a = 0.3 | lz4, a = 0.5 | zstd, a = 0.3 | Cold boot, no compression |
|---|---|---|---|---|---|
| Codex | ~590 | **1,429** | 1,147 | 1,676 | 142 |
| Claude Code | 370 | **853** | 690 | 990 | 143 |
| OpenCode | 104 | **235** | 191 | — | 82 |

The memory ceiling is no longer what limits a node. Three other limits now bind first:

- **The reservation cap.** Measured admission also caps Σ(ram_mb + 8) at 2 × 47,600, and at 2056 MB per VM that stops a node at 46 runs. The ADR has to separate the reservation an agent VM counts against from its `memory.max` burst limit. One option is to reserve the measured p95 footprint and keep 2048 MB as the burst limit. The alternative is a much higher cap on agent nodes, with eviction as the safety net.
- **CPU.** Active runs need vCPU.
- **The zswap pool.** It is capped at 25% of host RAM.

These figures describe a light scripted session (cat, rg, ls). They are the harness floor, not a typical run:

- Real runs that install packages, build or run tests add their tool processes on top.
- Longer conversations grow memory as cell D shows.

### Against the thresholds set in the plan

| Proposal | Result |
|---|---|
| Compress waiting runs as soon as they pause | **Memory threshold met with zstd** (`M_wait` 23–25% of `M_active` for Codex and Claude Code). **Not met with lz4** (34–36%). Latency is **not verified**: thaw is 9–37 ms, but refaults add 0.4–1 s to the next turn on this nested, loaded host. Needs a native host |
| Start runs from a warm preset snapshot | **Met** for Codex and Claude Code: shared 45–57%, first model call 6–8 s sooner, 230–270 MB less private memory. **Not met** for OpenCode (20% shared, no start-up gain) |
| guest-init cache drop before a pause | **Not measured.** It needs a guest-init hook, and Firecracker 1.7.0 here has no free page reporting |
| Quiet pause for a preset | **Codex is pausable** (7.6 ms per 2 s). Claude Code is pausable on average but spikes. **OpenCode is not pausable.** The VM-cgroup threshold needs native calibration |

### Changes from the plan

- **The harness image is drive0, not a full-rootfs layer.** vmmd copies a writable layer into the jail (tmpfs) when it cannot reflink it. A 1.6 GB layer per VM would have cost 1.6 GB of host RAM per run. This also confirms the ADR-20261027090010 choice of putting presets in drive0.
- **Compressed swap is zram behind zswap**, opened only for the run. Writeback is 0 on the tenant slice. During the first run, other sessions' idle memory (2.5 GB in `user.slice`) was swapped into it, so `memory.swap.max=0` was then set on every other cgroup. `run_memory.sh` now does this itself, and restores it afterwards.
- **Free page reporting** is off, because Firecracker 1.7.0 lacks it.
- **Cell F** was not run.
- **Codex 0.163 ignores `OPENAI_BASE_URL`.** The probe gives it a `config.toml` provider. The production preset needs the same.
- **The probe binds its control port before the provider.** guest-init's port normalisation (ADR-051) forwards :8080 to the first port an app binds.

## Why the model must be fake

The VM-level runs need a harness that is truly waiting on a model call
without a real provider. A fake provider runs inside the guest. It speaks
each harness's wire protocol, follows a fixed script of turns, and holds
every response until the test releases it. The harness sees a normal slow
model call and holds its real memory while waiting.

The provider runs in the guest for two reasons:

- Guests cannot reach host-private addresses (ADR-361 lateral deny).
- Job VMs get no outbound relay (`relayPreamble` resolves only executions and apps).

Its own footprint is removed by measuring a baseline VM. The baseline has
the same layer with the provider running and no harness started.

## Host

Checked 2026-10-18:

| Host | Status | Fit |
|---|---|---|
| fsn-4 (dedicated acceptance host) | unreachable: billing disabled on its GCP project | best: dedicated, zswap role can be applied, no other sessions |
| temporary GCP VM, e.g. n2-highmem-8 (64 GB, 200 GB pd-ssd), deleted after | needs owner approval, about $0.50/h for about 6 h | good for memory numbers; latencies are nested-virt |
| gregale-internal-test-1 | 2 vCPU, load 7, 16 GB (8 GB available), root disk 99% full, `/mnt/e2e-scratch` 37 GB free, acceptance lock held by another session, zswap off with no swap device | memory profile only, one VM at a time; enabling zswap changes a shared host |

All three are GCP VMs (the `hypervisor` CPU flag is set on internal-test-1).
Memory numbers are valid under nested virtualization, because cgroup charges,
page sharing and zswap are measured by the L1 host kernel. Pause, thaw and
restore latencies are not acceptance numbers there. They are recorded and
labelled nested-virt, and need a native EX44-class host before the ADR cites them.

Host requirements:

- Linux 6.8 or later for `memory.zswap.writeback` (internal-test-1 runs 7.0).
- systemd 253 or later.
- Firecracker 1.7.0 with the jailer.
- Kernel `/srv/fc/base/vmlinux-6.1.134` and base `/srv/fc/base/base-amd64.ext4`.
- 25 GB of free disk.
- 16 GB of RAM or more (64 GB for the density cell).

zswap is set with the existing role: `faas_zswap_enabled: true` with lz4,
`max_pool_percent` 25, writeback=0 on `faas-tenant.slice`, and a swapfile
for slots only. Do not set it by hand on a shared host.

## Artifacts to build first (laptop, unit-tested, no host needed)

1. **`fake_provider.py`**: a stdlib-only server on `127.0.0.1:8787`.
   - Protocols:
     - Anthropic Messages streaming (`/v1/messages`) for Claude Code and OpenCode;
     - OpenAI Responses streaming (`/v1/responses`) for Codex;
     - Chat Completions as a fallback.
   - Script: a JSON list of turns. Each turn is either a tool call or the final text.
   - Tool names come from the request's `tools` array: Claude Code `Bash` and `Read`; Codex shell or `exec_command`; OpenCode `bash`. So version drift picks the right tool or fails loudly.
   - Holds:
     - each response waits until `POST /release` or a hold timeout;
     - `GET /timeline` returns request arrival, hold start, release, stream end and the next request, per turn.
   - Keys: accepts any key. The harness gets `relay-placeholder-key`, as in `run_agent.py`.
2. **`agent_probe.py`**: the layer entrypoint (`app.json`, port 8080, runs as the app user).
   - `/warm`: reads every harness file once and runs `--version`, so the guest page cache holds them before Park.
   - `/run?preset=&script=`: starts the provider and the harness.
   - `/status`: phase, exit code and output digest.
   - It proxies `/release` and `/timeline`.
   - Telemetry is off: `DISABLE_TELEMETRY=1`, `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`, OpenCode's models fetch disabled. If OpenCode still blocks on models.dev, it becomes optional (open decision).
3. **`memsample.py` additions**:
   - `memory.zswap.current`;
   - from `memory.stat`: `zswap`, `zswapped`, `zswpin`, `zswpout`, `workingset_refault_anon`, `pgmajfault`;
   - `memory.swap.current`;
   - per Firecracker process `smaps_rollup`: Rss, Pss, Pss_Anon, Pss_File, Shared_Clean;
   - vCPU thread voluntary context switches.
4. **Scripts** (`scripts/agent-memory/*.json`):
   - **S1, PR triage:** 4 turns. Read `PR.diff`, `rg` in the repo, `git log -5`, then the final summary.
   - **S2, long context:** 20 turns, each tool output 50 KB. This measures memory growth per turn as the conversation history grows.
5. **Layer build without docker** (`build_agent_layer.sh`):
   - Pull `node:22-alpine` by the digest pinned in `images/agent-base.Dockerfile` from mirror.gcr.io with curl (registry API).
   - Unpack it, then run the Dockerfile's `apk` and `npm install -g` steps in a root chroot.
   - Add the probe, the provider, the scripts, and express at a pinned SHA under `/input`.
   - Write `upper/etc/faas/app.json`, then `mkfs.ext4 -d` into an ext4 layer of about 1.6 GB.
   - Fidelity gate: inside the chroot, before any VM boots, each preset must finish S1 against the fake provider with exit 0 and the scripted final text.
6. **`pkg/fcvm/agent_memory_metal_test.go`**:
   - `//go:build metal`, gated by `FAAS_TEST_AGENT_MEMORY=1`.
   - It uses the same Manager calls as `TestMetalFreezeThawCycle` and `TestMetalFreezeDensity`: `ColdBoot`, `Park`, `Wake`, `Freeze`, `Thaw`. It also samples the VM's cgroup scope at 1 Hz, and at 10 Hz for 5 s around each transition.
   - It writes JSON lines to `FAAS_TEST_AGENT_MEMORY_OUT`, and `summarize_runs.py` gains a memory report.

## Matrix

Guest size is 2048 MB (preset `ram_mb`), with 2 vCPU. Free page reporting
(ADR-907) runs with `FAAS_TEST_FREE_PAGE_REPORTING=1` unless a cell says off.
Each core cell runs 3 times.

| Cell | Presets | What runs | Answers |
|---|---|---|---|
| A. Active profile | codex, claude-code, opencode | S1, 3 s holds; cold boot vs restore from a warm preset snapshot; plus the baseline VM | private and file memory over time, peak, average; start-up time to the first model call |
| B. Waiting state | all three | S1, warm restore, one 120 s hold on turn 2. During the hold: (1) nothing, (2) pause only (ADR-20261027090012 quiet pause = `Freeze` without reclaim), (3) pause + reclaim with lz4, (4) as 3 with zstd. Then thaw and release | charge while waiting; zswap ratio (`zswapped / zswap`); reclaim time; thaw time; added latency until the harness's next request vs (1); refaults in the 5 s after thaw |
| C. Idle check | all three | the same holds, with no pause | CPU per 2 s window while waiting (ADR-20261027090012 pauses under 50 ms per window); vCPU wakeups. A harness that is not idle cannot be quiet-paused |
| D. Growth | all three | S2 | private memory per turn; whether the history pushes past the 256 MB floor and toward the 2048 MB guest |
| E. Sharing and density | the lightest and heaviest preset | K warm restores of one snapshot (K = 4 on 16 GB, 12 on 64 GB), staggered starts, holds on every turn, mode B3 | host-level memory (`MemTotal - MemAvailable` delta and Σ Pss) vs Σ cgroup charge, giving the shared fraction; peak host memory when all K are released at once |
| F. Guest cache drop | one preset | B3 after a guest `drop_caches` + free page reporting | extra memory returned. The probe runs as the app user, so this needs a test-only guest-init hook; if that is too invasive, record it as not measured |

About 2–3 hours of host time, plus about 1 hour for the layer build and the fidelity gate.

## What the numbers decide

These feed `N = 47,600 / (a·M_active + (1−a)·M_wait)` per node.

- `a` is the active share. It comes from the provider timelines with
  scripted holds. Real model latency was dropped with the keys, so `a` is
  swept from 0.2 to 0.5.
- `M_wait` comes from cell B. `M_active` comes from cells A and D, minus
  the sharing found in cell E.

The node is limited by the reservation cap as well as by measured memory.
Under measured admission, Σ(ram_mb + 8) ≤ 2× the ceiling
(`MeasuredAdmissionMaxOvercommitPercent` = 200). At 2056 MB per agent VM,
that cap stops a node at 46 runs, whatever compression saves. So the ADR has
to choose one of two things, and these numbers decide which:

- a different reservation for agent VMs (for example, the measured p95 active footprint);
- a higher cap on agent nodes only, with eviction and the burst-release peak from cell E as the safety argument.

Thresholds for each proposal in the memory-tiering ADR:

| Proposal | Goes in the ADR if |
|---|---|
| Compress waiting runs as soon as they pause | `M_wait` ≤ 25% of `M_active`, and the thaw adds ≤ 50 ms p95 per model call on a native host |
| Start runs from a warm preset snapshot | the shared fraction is ≥ 30% of the restored footprint, or the time to the first model call improves by ≥ 1 s |
| guest-init cache drop before a pause | cell F returns ≥ 20% more memory than B3 |
| Quiet pause for a preset | cell C stays under 50 ms of CPU per 2 s window while waiting. Otherwise that preset is listed as not pausable |

These are not part of the plan: KSM (cross-tenant side channels) and swap
writeback to NVMe (tenant memory on disk). zswap writeback stays 0.

## Safety and cleanup

- No provider keys. Harnesses get the placeholder. The VMs make no third-party calls. The only external fetches are the npm and registry pulls at build time.
- Hold `/var/lock/faas-builder-acceptance.lock` for every VM run. Never run `scripts/ci/run-native-e2e.sh`. Stop if another session's Firecracker VMs are running.
- Stage everything under one directory (`/mnt/e2e-scratch/agent-memory-<run>` or the host's scratch disk). Name the test cgroups `agent-mem-*`.
- After each session:
  - `deploy/scripts/leakcheck.sh` reports zero netns, TAPs, jail uids and cgroups;
  - no Firecracker process from this run remains;
  - the staging directory is removed;
  - zswap and swap are restored to their state before the run (or the role is rolled back on a dedicated host);
  - on a temporary VM, the VM is deleted.
- Results (JSON lines and a summary) go into this directory's README. Raw samples stay off the repo.

## Order

1. Build and unit-test artifacts 1–4 on the laptop.
2. Owner picks the host (and approves the temporary VM or the zswap change, if chosen).
3. Layer build plus the fidelity gate on the host.
4. Cells A and C, then B, D, E and F.
5. Clean up and record the numbers.
6. Recompute the density table, then draft the memory-tiering ADR.
