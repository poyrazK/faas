# rc.119 public-beta readiness evidence — 2026-09-11

This directory records the final SSD restore acceptance cohorts and the
operational checks for release `v0.1.18-rc.119`, commit
`bca75d649ffabe5d40c466c246ed60dacad3bcc6`.

## SSD snapshot restore gate

The acceptance interval is the scheduler's
`wake.boot_started.at -> wake.boot_completed.at`. It excludes Cloudflare,
Internet transit, client latency, and application execution. Every sample ran
on `faas-compute-node-1`, node ID
`cc461882-cba6-4182-8f26-e67c314361dc`, with `/dev/sdb` reporting `ROTA=0`
and mounted at `/srv/fc` as the `pd-ssd` snapshot/cache disk.

| Cohort | n | HTTP 200 | min | average | p50 | p90 | p95 | p99 | max |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Sequential | 100 | 100 | 179.970 ms | 209.802 ms | 205.533 ms | 234.511 ms | 240.765 ms | 273.348 ms | 337.836 ms |
| Two-restore bursts | 30 | 30 | 189.818 ms | 267.073 ms | 260.923 ms | 324.507 ms | **335.219 ms** | 366.242 ms | 366.242 ms |
| Combined | 130 | 130 | 179.970 ms | 223.018 ms | 211.218 ms | 271.340 ms | **297.979 ms** | 337.836 ms | 366.242 ms |

All 130 unique wakes used `method=restore`, ran the exact rc.119 commit, and
resolved kernel, base, and main artifacts from the node-local cache. There
were no cold-boot fallbacks. The combined cohort has one sample at or above
350 ms, while both the sequential and burst cohorts pass the required p95
below 350 ms.

The burst cohort contains 15 waves of two simultaneous public restores after
natural ten-second idle parking. The slowest platform sample was 366.242 ms:
VMMD used 351 ms total, including a 239 ms restore breakdown (98 ms snapshot
staging and 96 ms resume hook). It had zero scheduler queue depth, zero
admission concurrency, zero restore-gate wait, and cache hits for all three
artifacts.

The canonical inputs and results are:

- `request-correlation.tsv`, `vmmd-wake-ok.ndjson`,
  `platform-events.ndjson`, and `summary.json` for the sequential cohort.
- `burst-request-correlation.tsv`, `burst-vmmd-wake-ok.ndjson`,
  `burst-platform-events.ndjson`, and `burst-summary.json` for the burst
  cohort.
- `combined-summary.json` for the aggregate result.
- `analyze.py` rebuilds either cohort from the immutable correlation and VMMD
  rows plus the platform event API. `collect-bursts.py` documents the burst
  fixture procedure.

## Operational checks

The following checks were run against production on 2026-09-11 after the
rc.119 control-plane and SSD-node rollouts:

- `/opt/faas/current` resolved to the exact rc.119 commit on both active
  hosts. Both hosts had zero failed systemd units.
- Control-plane readiness returned 200 for apid, schedd, gatewayd-public,
  githubd, and meterd. SSD-node readiness returned 200 for builderd, imaged,
  vmmd, gatewayd-internal, and the compute data listener.
- `gregalectl doctor --deep` reported 11 checks OK, six warnings, and zero
  errors. Release signatures and retained-release vulnerability counts were
  valid. The warnings are missing optional archive-shipper credentials and
  absent SBOM baselines for retained releases; PostgreSQL off-host backup
  credentials are configured separately and working.
- The public `https://api.gregale.dev/healthz` route returned HTTP/2 200
  through Cloudflare. `api.gregale.dev` and a wildcard hostname resolved to
  the Cloudflare addresses, and the served `gregale.dev` certificate was
  valid from 2026-07-29 through 2026-10-27.
- Both PostgreSQL backup timers were active. The off-host push verified three
  backups and pruned one local backup. An isolated restore plus WAL replay
  passed in 16 seconds: accounts 27/27, apps 335/337, and instances
  4511/4660, all above the required 95% recency ratio.
- `faas-compute-node-2` remained intentionally terminated. Only the SSD node
  was active for placement and acceptance.
- The two temporary burst fixtures were deleted; the account app inventory
  returned to its 33-app baseline.

The audit also found one old Firecracker process whose durable instance was
parked but whose jail directory had already disappeared. That process exposed
a discovery gap in the startup orphan sweep and is tracked by #1897. The code
change accompanying this evidence discovers verified process-only candidates
from `/proc` while preserving the age and durable-liveness safety gates.
