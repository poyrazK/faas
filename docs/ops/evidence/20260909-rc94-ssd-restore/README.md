# v0.1.18-rc.94 SSD snapshot restore acceptance

The deployed release completed 100 out of 100 snapshot restores on the GCP SSD
compute node. The platform interval had p95 **137.756 ms** and maximum **149.067
ms**. No sample reached the 350 ms SLO limit.

| Platform interval | Result |
| --- | ---: |
| samples / unique wake IDs | 100 / 100 |
| minimum | 73.094 ms |
| average | 99.011 ms |
| p50 | 94.795 ms |
| p90 | 131.502 ms |
| p95 | **137.756 ms** |
| p99 | 148.229 ms |
| maximum | 149.067 ms |
| samples >= 350 ms | 0 |

## Scope and method

- Release tag: `v0.1.18-rc.94`
- Release commit: `86968d3d8d39670cc553f397358b70938bed49bc`
- GCP project: `project-5ae37259-04cf-4070-bef`
- Node: `faas-compute-node-1`
- Node ID: `cc461882-cba6-4182-8f26-e67c314361dc`
- Artifact disk: `/dev/sdb`, `ROTA=0`, GCP `pd-ssd`
- Fixture: `public-val-go-persistent-0906`, Go 1.24 Alpine, 256 MiB,
  one maximum instance, ten-second idle timeout
- Load shape: 100 sequential public requests. The normal idle reaper parked the
  instance before each next request. No operator `park` request was used.
- SLO interval: schedd `wake.boot_started.at` through schedd
  `wake.boot_completed.at` for the same wake ID.
- Percentiles: nearest-rank over all 100 samples.

Every completion reported method `restore`. Every kernel, base and main artifact
resolution was a zero-millisecond `cache_hit`. All 100 wakes ran on the SSD node;
`concurrency_at_admit` and `queued_count` were zero for this idle cohort.

The public request times are retained as correlation context only. They include
Cloudflare, TLS, client network, gateway proxying and application execution and
therefore are not the platform snapshot-restore SLO. One request returned a
`hot` public header after the edge retry while its newly-created instance and
canonical event stream recorded a distinct restore wake. The correlation was
recovered from the new instance wake ID and the sample is included. Its platform
interval was 118.741 ms.

## Files

- `summary.json` contains the asserted release, node, disk, distribution and
  cache-source summary.
- `request-correlation.tsv` maps cycle, wake ID and contextual public duration.
- `platform-events.ndjson` retains the raw wake event streams and the derived
  platform duration for every cycle.
- `analyze.py` reproduces the statistics from the request-correlation file by
  fetching each canonical wake timeline. It exits non-zero unless all 100 wakes
  are unique SSD restores, all artifacts are cache hits, and p95 is below 350 ms.
