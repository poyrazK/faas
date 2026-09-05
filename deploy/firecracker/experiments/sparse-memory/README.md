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

File snapshot restore allocates private anonymous guest memory, advises
transparent huge pages, and copies populated file extents before KVM can
access the mapping. Sparse holes remain zero. Tests cover region offsets,
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

Before any fleet adoption, validate dense inputs and memory pressure, longer
traffic runs, other guest sizes/runtimes, and the full lifecycle suite.
Anonymous eager copying can charge more pages to each VM than shared file
mappings. The small fixture's sampled usage does not establish behavior at
the memory fence. Unsupported sparse seeking copies the complete region;
THP availability also depends on the host. Do not disable seccomp, raise the
VM memory limit, or discard slow samples to make the experiment pass.

Temporary canaries must drain guests and restore the previous VMMD executable
and PATH selection after measurement. No Ansible pin or systemd service is
changed by these files. `manifest.json` records the tested artifact's source,
patch and binary provenance; build paths can affect binary reproducibility.
