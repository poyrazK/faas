# ADR-910: Per-plan block rate limits

- Status: Accepted
- Date: 2026-10-12
- Related: ADR-158, ADR-907, ADR-983, spec §4.6, §11
- Amends: spec §4.6 (drive1 carries a rate limiter)

## Context

Every VM on a node shares the host disk. A VM's writable app layer
(`drive1`) has no host-side I/O limit: guest `io.weight` (ADR-158) only
orders I/O inside one guest, and the host cgroups set no `io.max`.
Firecracker can put a token-bucket rate limiter on any block device, and
Gregale used none.

Measured on the n2 test node (Firecracker 1.17, guest 6.18, persistent disk)
with two Node VMs on one host disk. The victim timed 300 4 KiB writes, each
followed by `fdatasync`, while its neighbour rewrote a 64 MB file in a loop:

| neighbour | neighbour throughput | victim fdatasync p50 / p99 | victim 128 MB write |
|---|---|---|---|
| idle | — | 0.52–0.68 / 0.78–1.56 ms | — |
| unlimited | 1,238–1,455 MB/s | 1.64–2.15 / 11.4–11.8 ms | 115–985 ms |
| 200 MB/s, 8,000 IOPS | 194–211 MB/s | 0.54–0.60 / 2.9–3.4 ms | 47–63 ms |
| 100 MB/s, 4,000 IOPS | 101 MB/s | 0.62–0.63 / 3.1–3.2 ms | 51–56 ms |
| 50 MB/s, 2,000 IOPS | 49–50 MB/s | 0.51–0.61 / 2.0–3.7 ms | 53–70 ms |

An unlimited neighbour made the victim's synced writes about ten times
slower at p99. Any limit up to 200 MB/s kept the victim's p50 at its idle
level.

Also measured, and rejected:

- **`io_engine: Async` (io_uring).** Three alternating rounds against Sync:
  cold boot, a 256 MB guest write and read, and `fdatasync` latency were
  all within noise. Cold snapshot restores were slower, 1.9–5.3 s against
  1.8–2.4 s. Gregale keeps the default Sync engine.
- **Static CPU templates** (T2S, T2, C3). They showed no measurable cost:
  the CPU-bound request took 7.1–7.5 ms with every template, and boot and
  restore were within noise. They only matter for snapshot portability
  across CPU models, which a single-model fleet does not need yet.

## Decision

1. Every app VM's writable app-layer drive gets a Firecracker rate limiter
   from its plan, `api.BlockRateLimitPerPlan`:

   | plan | bandwidth | operations |
   |---|---|---|
   | Free | 50 MB/s | 2,000/s |
   | Hobby | 100 MB/s | 4,000/s |
   | Pro | 200 MB/s | 8,000/s |
   | Scale | 200 MB/s | 8,000/s |

   Each bucket also has a one-time burst
   (`BlockRateLimitBurstMB` = 256 MiB, `BlockRateLimitBurstOps` = 20,000),
   so startup and short bursts are not throttled.
2. The shared read-only base and sidecar drives are not limited. They are
   read mostly from the host page cache, and pmem bases (ADR-908) bypass
   virtio-blk entirely. Builder VMs are not limited either: their slots
   already bound them.
3. A cold boot puts the limiter in the VM's config. A snapshot restore sets
   it with `PATCH /drives/{id}` after the guest is ready, so it adds nothing
   to wake latency. This also covers snapshots captured before this ADR. A
   failed PATCH only logs.

## Consequences

- A workload that writes more than its plan's sustained rate after the
  burst slows to that rate. Guest `/tmp` is tmpfs and is not affected.
- Raising a plan's limit takes effect on the next boot or restore.
- Network rate limits are not part of this decision; tenant egress stays
  with nftables. On the n2 node, the host downloaded from a neighbour VM at
  262–417 MB/s, which raised a victim VM's request latency from p50 1.2 /
  p99 3.4–4.8 ms to 2.4–3.3 / 19–31 ms. Limiting the neighbour's guest tx
  to 50 MB/s only partly helped (1.9–2.2 / 10.6–22 ms). On that two-vCPU
  host the test's own download loop competes for the same CPUs, so this
  needs a larger host before it can decide a network limit.

## Verification

- Unit: `pkg/fcvm/block_rate_limit_test.go` covers which drives get the
  limiter, the Firecracker JSON shape, and the restored drive id.
- Metal: `TestMetalBlockRateLimit` checks the limiter in `/vm/config` after
  a cold boot and after a restore, and that guest writes slow to it.
- On the n2 node, `TestMetalBlockRateLimit` set Pro's limit to 20 MB/s for
  the test. A 512 MB guest write ran at 130 MB/s without a limit, then at
  33 MB/s after a cold boot and 32 MB/s after a restore: the 256 MiB burst
  followed by the limit. Both times `/vm/config` showed the limiter on the
  layer drive.
