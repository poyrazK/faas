# v0.1.18-rc.98 SSD snapshot restore acceptance

The deployed release completed 100 out of 100 snapshot restores on the GCP SSD
compute node. The full platform interval had p95 **296.659 ms**, p99 **333.886
ms**, and maximum **335.953 ms**. Every sample was below the 350 ms target.

| Platform interval | All samples | Sequential | Three-way bursts |
| --- | ---: | ---: | ---: |
| samples | 100 | 52 | 48 |
| minimum | 99.119 ms | 99.119 ms | 127.926 ms |
| average | 164.142 ms | 118.916 ms | 213.136 ms |
| p50 | 142.700 ms | 113.878 ms | 197.282 ms |
| p90 | 263.190 ms | 142.700 ms | 310.421 ms |
| p95 | **296.659 ms** | 160.654 ms | 320.970 ms |
| p99 | 333.886 ms | 193.244 ms | 335.953 ms |
| maximum | 335.953 ms | 193.244 ms | 335.953 ms |
| samples >= 350 ms | 0 | 0 | 0 |

## Scope and method

- Release tag: `v0.1.18-rc.98`
- Release commit: `76ff511e15d64df2ba703ad25a700381af8f3028`
- GCP project: `project-5ae37259-04cf-4070-bef`
- Node: `faas-compute-node-1`
- Node ID: `cc461882-cba6-4182-8f26-e67c314361dc`
- Host: `n2-standard-4`, four vCPUs
- Artifact disk: `/dev/sdb`, `ROTA=0`, GCP `pd-ssd`
- Restore concurrency: three, the configured in-capacity limit for this host
- Workloads: Go persistent, Go oneshot, Node.js 24, and Python 3.13
- Load shape: 52 sequential restores followed by 16 simultaneous three-app
  waves. The burst triplets rotated across all four workloads.
- SLO interval: schedd `wake.boot_started.at` through schedd
  `wake.boot_completed.at` for the same wake ID.
- Percentiles: nearest-rank over the stated sample set.

Every request began with its application instance parked by the normal idle
reaper. No application instance was warm, and no operator park request was
used. The host's prepared network pool and SSD artifact cache were warm after
the post-deploy canary. Prepared network namespaces are platform capacity, not
running application instances.

Every completion reported method `restore`. Kernel, base, and main artifact
resolution reported `cache_hit` in all 100 timelines. Every wake ran on the
same SSD node. The three-way waves reached the configured restore concurrency
without gate wait. A fourth simultaneous restore is above this host's restore
capacity and waits at the gate; it is outside this acceptance cohort.

Runtime-specific platform maxima were 269.038 ms for the Go persistent
fixture, 310.421 ms for Go oneshot, 335.953 ms for Node.js 24, and 333.886 ms
for Python 3.13.

Public request times are retained as correlation context only. They include
Cloudflare, TLS, client network, gateway proxying, and application execution,
so they are not the platform snapshot-restore SLO. Their p95 in this run was
848.589 ms.

## Files

- `summary.json` contains the asserted release, node, disk, distributions,
  cache-source counts, and shape/runtime breakdowns.
- `request-correlation.tsv` maps each sample, load shape, app, wake ID, HTTP
  status, wake header, and contextual request duration.
- `platform-events.ndjson` retains every raw wake timeline and the derived
  platform duration.
- `collect.py` reproduces the 52 sequential plus 16 rotating three-way waves.
- `analyze.py` fetches each canonical wake timeline and reproduces the
  statistics. It exits non-zero unless all 100 wakes are unique SSD restores,
  all artifacts are cache hits, and platform p95 is below 350 ms.
