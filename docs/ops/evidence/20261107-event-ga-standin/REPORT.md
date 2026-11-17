# Event GA gate on a GCP stand-in for the reference host (2026-11-07)

The event GA gate
([ADR 20261029093000](../../../adr/20261029093000-event-ga-gate.md)) asks for
V12 at the target and a failover drill between two hosts on the OVH reference
host. No OVH host is ordered yet: ADR-1344 §8 sizes the control node (KVM,
NVMe, at least 32 GB of RAM) but leaves the machine and datacenter to confirm.
These runs use GCP machines of that class instead. The ADR's amendment of
2026-11-07 records how far they count.

## Hosts

| Role | Machine | CPU, RAM | Data disk |
|---|---|---|---|
| Primary and load (`event-ga-ref-2`) | n2-standard-16, us-east1-b | 16 vCPU, 64 GB | two local NVMe SSDs, RAID 0 |
| Streaming standby (`event-ga-ref-standby`) | n2-standard-4, us-east1-b | 4 vCPU, 16 GB | pd-ssd |

Both: Ubuntu 24.04, PostgreSQL 16.15 with the scripts' settings (defaults
plus `max_connections=200`), Go 1.26.10, on the isolated
`gregale-internal-tests` network. Nothing else ran on them. The project's
32-CPU global quota left room for only one 16-vCPU host, so the standby is
smaller; launch also pairs the primary with a smaller GCP warm standby
(ADR-1344 §8). Both hosts were deleted after the runs.

## V12: the target is met with room to spare

Build `ceaabd82` (main). The same protocol as the test-node runs
([2026-11-05](../20261105-event-ga-v12/REPORT.md)), with the sweep extended
to 200 events/s.

| Run | 20/s | 35/s | **50/s** | 65/s | 80/s | 100/s | 150/s | 200/s |
|---|---|---|---|---|---|---|---|---|
| `sweep-1` | 100 %, p95 23 ms | 100 %, p95 21 ms | **100 %, p95 19 ms ✅** | 100 %, p95 20 ms | 100 %, p95 20 ms | 99.9 %, p95 36 ms | 99.6 %, p95 0.23 s | 33 %, p95 31 s |
| `sweep-2` | 100 %, p95 26 ms | 100 %, p95 20 ms | **100 %, p95 19 ms ✅** | 100 %, p95 23 ms | 100 %, p95 22 ms | 99.9 %, p95 31 ms | 99.5 %, p95 0.37 s | 36 %, p95 27 s |
| `standard` (2,000-event stages first) | — | — | **100 %, p95 21 ms ✅** | — | — | — | — | 31 %, p95 36 s |
| `ordered` (20 keys; target 20/s) | **100 %, p95 31 ms ✅** | — | 100 %, p95 24 ms | — | — | — | — | — |

Every run settled with nothing pending. The pipeline sustains 150 events/s,
three times the target, and the ordered lanes sustain 50/s, 2.5 times theirs.

| Stage | test node, 2026-11-05 | stand-in |
|---|---:|---:|
| Publish (2,000 events) | 527/s | 3,172/s |
| Fanout over a 2,000-event backlog | 71/s | 121/s |
| Drain | 121/s | 526/s |
| Webhook delivery, one account (#6692 included) | 18/s | 176/s |

**Finding: past the knee, throughput collapses instead of levelling off.**
Offered 200/s, the pipeline delivered 61–72/s, less than half of what it
sustained at 150/s, and p95 grew past 25 s. Once a backlog forms, fanout
works through it at about 120/s (the `standard` fanout stage), so routing
falls behind publishing and the backlog compounds. The per-account caps
(ADR-20261023215834, at most 50 % of the sustained rate per account) keep
one customer from driving the platform there, but a mix of accounts above
150/s would. This is the next performance lever and does not block the gate.

`pg_stat_statements` was not created in the fresh cluster, so this run has
no per-statement profile (`<run>-statements.tsv` left out); the test node's
2026-11-05 profile stands.

## Failover drill: no loss, routing back in 14 s

Build `570dff38` (main). The drill test (`TestEventFailoverDrill`) between
the two hosts: the primary on `event-ga-ref-2`, a streaming standby
(`pg_basebackup -R -X stream`) on `event-ga-ref-standby`, and the load from
`event-ga-ref-2`. Each run publishes at 50 events/s for 30 s, crashes the
primary with an immediate stop (no checkpoint), promotes the standby over
ssh, then routes on the new primary across the network.

| Run | Acknowledged | Survived | Lost | Crash to first accepted publish | Receipts settled | Routing settled in |
|---|---:|---:|---:|---:|---:|---:|
| 1 | 1,499 | 1,499 | 0 | 1.60 s | 1,500 / 1,500 | 13.2 s |
| 2 | 1,499 | 1,499 | 0 | 1.61 s | 1,500 / 1,500 | 13.7 s |
| 3 | 1,499 | 1,499 | 0 | 1.60 s | 1,500 / 1,500 | 14.2 s |

The extra receipt is the first publish accepted after the promotion. Against
the single-host drill of 2026-10-29: the same zero loss, crash to first
accept 1.6 s instead of 1.1–1.5 s (the standby is now across the network),
and routing settled in 13–14 s instead of 41–57 s.

This exercises the database failover the gate measures. It does not replace
the whole-platform `ControlPlaneFailover.md` drill (DNS, daemons, the RTO of
15–30 minutes), which needs the launch hosts.

## Files

- `v12-commit.txt`, `drill-commit.txt`: the commits measured.
- `<run>-event-pipeline.json`, `standard-webhook-delivery.json`: V12
  reports; `<run>-load-start.txt`, `<run>-window.txt`: load and UTC window.
- `drill-run-<n>.json`, `drill-run-<n>-load.txt`, `drill-run-<n>-window.txt`:
  the drill reports.
