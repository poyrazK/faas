# SSD snapshot restore canary — 2026-09-05

The basic Node 22 function met the idle internal-gateway target in this cohort:
20/20 HTTP 200 responses, full-response p95 **281.22 ms**, maximum **324.60 ms**.
Every request began with zero live instances; all 20 created distinct instances
by snapshot restore and returned distinct guest UUIDs. There was no cold-boot
fallback or warm-instance reuse.

| Timed interval | Samples | p50 (ms) | p95 (ms) | Maximum (ms) |
| --- | ---: | ---: | ---: | ---: |
| Snapshot restore through readiness | 20 | 73 | 104 | 126 |
| VMMD wake including network setup | 20 | 74 | 124 | 126 |
| Internal gateway request to first response headers | 20 | 216.66 | 281.20 | 324.57 |
| Internal gateway request to complete response | 20 | 216.69 | 281.22 | 324.60 |

## Measurement boundary

The client ran on the GCP control-plane node and called the SSD compute node's
internal gateway over the VPC. This includes admission, scheduling, forwarding,
restore and the function response. It excludes the public TLS edge. The compute
node was n2-standard-4 (4 vCPU, 16 GB RAM) with a 100 GB pd-ssd, XFS reflinks,
and a tmpfs jail directory. The HDD compute node is excluded.

There was no background traffic. The unused-network cache was set to three
entries: 19 hits and one miss. Those entries contain host network objects only,
with no guest process or resident guest memory. Percentiles use nearest rank;
all attempts are included. Twenty requests are a small acceptance cohort,
not evidence of sustained normal-traffic or burst p95. The latest earlier
30-request burst cohort, before the timer correction, had full-gateway p95
623.25 ms. Burst acceptance must be repeated after the correction.

## Changes exercised

- Snapshot-origin/ready-replica placement and initial snapshot locality.
- Batched network setup, exclusively claimed unused networks, and pinned bridge
  identity with namespace and allocation cleanup.
- A lightweight jail mount helper and lazy OpenAPI conversion to reduce helper
  startup overhead.
- Mandatory host entropy injection and CRNG reseeding; optional hardware entropy
  reads no longer block readiness. Resume accept retries and advisory framework
  readiness avoid additional stalls.
- Gateway orphan-wake recovery, activity flush cadence, stream header batching,
  first-response timing/correlation and scrapeable wake metrics.
- The [Firecracker timer restore correction](../../deploy/firecracker/README.md):
  restore deadline MSRs after clock MSRs, including legacy snapshots.

## Timer failure regression

An idle HTTP 503 was reproduced from retained snapshot inputs without the
scheduler or gateway. The final Firecracker 1.7.0 patch passed three register
ordering/value-preservation unit tests and 30/30 retained-input metal restores
with 30 distinct guest UUIDs and no cold fallback. The private metal run's wake
plus function-response p95 was 314.40 ms; this interval excludes the gateway
and must not be substituted for the gateway result above. Resource leak checks
passed. This was a focused test, not the entire legacy metal suite.

The artifact was built with pinned Rust 1.76.0 and the original Cargo.lock and
seccomp filters. Only the x86 vCPU source file changed. Source, patch and binary
hashes are recorded in the [canary manifest](../../deploy/firecracker/tsc-canary-manifest.json).
Only the SSD VMMD canary selects this binary; the fleet runtime pin is unchanged.
The [timer ADR](../adr/150-firecracker-tsc-restore-order-canary.md) describes rollback.

These measurements were taken from the isolated canary build based on
`c8a4cfe02fdf4efe035629a43b430380b01ed90c`, before integrating newer main changes
for this PR. The rebased PR itself has not been redeployed or rebenchmarked.

## Evidence

- [Gateway aggregate](evidence/20260905-ssd-restore/single-tsc-fixed-20-measurement-summary.json)
- [All 20 client samples](evidence/20260905-ssd-restore/single-tsc-fixed-20.jsonl)
- [Restore method and instance identity](evidence/20260905-ssd-restore/single-tsc-fixed-20-restores.jsonl)
- [Final retained-input metal summary](evidence/20260905-ssd-restore/tsc-final-summary.json)
- [Firecracker source audit](evidence/20260905-ssd-restore/final-source-audit.json)

Remaining acceptance: normal traffic and bursts within host capacity, public-edge
latency, and the full x86 metal suite on the integrated release candidate.
