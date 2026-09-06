# Sparse anonymous restore experiment

This is an experimental Firecracker patch, not the fleet runtime selection.
It targets the pinned 1.7.0 source and Rust 1.76.0 toolchain described in
`../../README.md`. Apply `../../tsc-restore-order.patch` and `memory.patch`
to a pristine checkout at `76542f598b7a7fd59559949070aa6b0c4e68523d`, keeping
the original dependency declarations and Cargo.lock. Then run:

```sh
cargo test --locked --release --target x86_64-unknown-linux-musl -p vmm --lib sparse_restore_tests
cargo test --locked --release --target x86_64-unknown-linux-musl -p vmm --lib legacy_restore_msr_tests
cargo build --locked --release --target x86_64-unknown-linux-musl -p firecracker
```

Sparse File snapshot restore allocates private anonymous guest memory, advises
transparent huge pages, and copies populated file extents before KVM can
access the mapping. Sparse holes remain zero. Before allocation, the runtime
conservatively estimates the THP footprint of those extents. If it cannot
leave 25% of guest RAM uncharged (capped at 32 MiB), or sparse seeks are
unsupported, restore uses the original lazy private-file mapping. Tests cover region offsets,
logical bytes, isolation between restores and the backing file, and malformed
lengths. Snapshot format, cold boot and seccomp filters remain unchanged.

On the GCP SSD host, 120 interleaved restores of the same prepositioned Node 22
snapshot produced component p95 of 420.50 ms with the installed file mapping
and 148.17 ms with this patch (60 samples per backend, bursts of three).
Both measurements include the complete guest HTTP response and exclude
scheduler/gateway coordination and prior sparse-file preparation. UUID,
restore-method and resource leak checks passed. CPU and memory fences were
unchanged; sampled candidate cgroups stayed below their 264 MiB memory limit.

The first full gateway comparison with this patch and sparse storage measured
p95 of 328.38 ms for single wakes, 347.55 ms for two wakes with background
traffic, and 371.75 ms for three wakes. All 35 requests used new restored
instances. This failed the three-wake target; an 80 ms network-cache miss in
the slowest request motivated proactive spare refresh in ADR-149. The current
runtime successfully restored three snapshots subsequently saved by the
candidate, confirming that tested rollback direction without cold fallback.

With proactive spare refresh, a second 35-request gateway cohort passed:

| Scenario | n | Full response p50 | p95 / max |
| --- | ---: | ---: | ---: |
| Single wake | 10 | 185.67 ms | 283.22 ms |
| Two wakes plus traffic on a third guest | 10 | 221.55 ms | 245.02 ms |
| Three wakes | 15 | 277.68 ms | 324.85 ms |

All requests used new snapshot-restored instances, with zero errors or cold
fallbacks. The background guest served 1,462 successful requests. Percentiles
use nearest rank and retain first-use samples. Every target had zero live
instances before its request. Measurements run from the control plane over
VPC to the SSD internal gateway and exclude public TLS/Cloudflare/Internet
transport and prepositioning. `gateway-samples.csv` retains each timed row;
`gateway-summary.json` records the cohort results. This is a small smoke
cohort, not a longer-term SLO acceptance. The temporary canary was rolled back
and fixtures parked afterward. No new optimization remains active in services.

Storage publication and the network-refresh fix are isolated changes on top of
`4a742818d7f8181c376003fb82f56c4c5ebaef73`; these hardware results do not include
subsequent changes merged into main. Focused SSD metal tests cover network
ownership/refresh, bridge identity, fresh/reused network policy and 100
restores, followed by leakcheck. Storage race/Linux tests and prepared-network
race tests pass. Full fcvm lint reports two pre-existing gosec findings in
unchanged vmm.go; lint restricted to the new changes reports no issues.

Before any fleet adoption, resolve the queue-state rejection described below,
complete longer traffic runs, validate broader workloads/runtimes and sustained
host pressure, and run the full lifecycle suite.
Anonymous eager copying can charge more pages to each VM than shared file
mappings. The small fixture's sampled usage does not establish behavior at
the memory fence. Unsupported sparse seeking retains lazy file mapping;
THP availability also depends on the host. Do not disable seccomp, raise the
VM memory limit, or discard slow samples to make the experiment pass.

Temporary canaries must drain guests and restore the previous VMMD executable
and PATH selection after measurement. No Ansible pin or systemd service is
changed by these files. `manifest.json` records the tested artifact's source,
patch and binary provenance; build paths can affect binary reproducibility.

## Memory headroom guard and extended acceptance

The original binary `6c1dba40...` is withdrawn: a 120-restore run captured two
cgroup OOM kills after guests had already returned successful HTTP responses.
Dense 256 MiB memory files charged the full 264 MiB fence; request success
alone missed the subsequent guest failures. No memory limit was increased.

The guarded binary in `manifest.json` estimates the resident footprint before
copying. It includes THP expansion and keeps dense inputs on the original lazy
file backend. Rust tests cover sparse bytes/offsets/isolation on the eager path,
dense and THP-scattered layouts on the lazy path, and malformed inputs.
The source/toolchain/TSC patch/seccomp pins remain unchanged.

`TestMetalSnapshotMemoryHeadroom` makes byte-identical sparse and dense files,
restores three guests concurrently, holds them after responding, checks a second
HTTP response with the same guest UUID, and records each live memory fence.
It rejects missing guests, nonzero hard-limit/OOM counters, changed memory
limits, or cold fallback. The SSD 256 MiB run passed 60 restores per input:

| Input | Restore + complete guest response p95 | Maximum | Max/OOM/OOM-kill counters |
| --- | ---: | ---: | --- |
| Sparse | 138.97 ms | 141.97 ms | 0 / 0 / 0 |
| Dense, lazy fallback | 440.67 ms | 459.15 ms | 0 / 0 / 0 |

All 120 post-response checks and fence captures passed; leakcheck passed.
A further 12 checks each at 128, 512 and 1024 MiB also passed with zero
hard-limit/OOM counters (`memory-headroom-ram-matrix.json`). The 1 GiB
case needed 2 GiB of private snapshot scratch space; guest fences were unchanged.
`memory-headroom-summary.json` records the aggregates. File-backed page-cache
ownership makes cgroup residency incomparable to total host memory use.
Dense input is a correctness fallback and does **not** meet the 350 ms target.

To run the headroom test, use a private network/mount namespace with empty
`/run/netns` and `/srv/fc/jail` (at least 2 GiB scratch for the 1 GiB case),
current static VMMD helper and Node 22 fixture,
and the same-basename candidate runtime. Set `FAAS_TEST_KERNEL`,
`FAAS_TEST_BASE_ROOTFS`, `FAAS_TEST_LAYER_ROOTFS`, `FAAS_TEST_VMMD_HELPER`,
`FAAS_TEST_RESTORE_RUNTIME_DIR`, and a writable
`FAAS_TEST_RESTORE_ACCEPTANCE_DIR`. Build `./pkg/fcvm` with `-tags metal`, then
run `^TestMetalSnapshotMemoryHeadroom$`. The default is 40 bursts at 256 MiB;
`FAAS_TEST_RESTORE_RAM_MB` selects a four-burst matrix case at
128/256/512/1024 MiB. The six top jail UIDs must be unused before starting.

An extended gateway soak of merged VMMD `911efd86` plus the guarded runtime
was stopped after 27 single-wake requests, including a 2.55 s cold fallback.
Full-response p95 was 350.30 ms with all samples retained;
`gateway-guard-incomplete-summary.json` marks this failed/incomplete cohort. Firecracker rejected the snapshot
with `Snapshot state contains invalid queue info` for a block device.
The root cause is not established; the memory guard is not claimed to fix it.
All failed samples are retained. Stable services were restored and fixtures
parked. Permanent rollout and the full-wake SLO acceptance remain blocked.

A subsequent 60-generation local save/restore chain passed with the same
guarded runtime and no registry/shared cache. This narrows follow-up work
but does not establish the root cause of the gateway rejection.
