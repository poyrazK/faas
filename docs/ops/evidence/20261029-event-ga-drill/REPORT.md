# Event failover drill, provisional (2026-10-29)

`make drill-event-failover` ([event-failover-drill.md](../../event-failover-drill.md)),
three runs on `gregale-internal-test-1` (2 vCPU, shared, 1-minute load 3.4–6.1
at the start of each run). It was built from `5fa05503`: the GA-gate commit on
top of #5836 (backlog throughput), because main did not build at the time
(`pkg/imaged` declared `scannerIdentity` twice after #5776). This is the
provisional drill the GA gate allows
([ADR 20261029093000](../../../adr/20261029093000-event-ga-gate.md)); promotion
needs the `ControlPlaneFailover.md` drill between the OVH hosts.

Each run publishes at 50 events/s for 30 s, crashes the primary with an
immediate stop, promotes its streaming standby on the same host, then
routes on the new primary.

| Run | Acknowledged | Survived | Lost | Crash to first accepted publish | Receipts settled | Routing settled in |
|---|---:|---:|---:|---:|---:|---:|
| 1 | 1,499 | 1,499 | 0 | 1.16 s | 1,500 / 1,500 | 56.7 s |
| 2 | 1,499 | 1,499 | 0 | 1.10 s | 1,500 / 1,500 | 41.1 s |
| 3 | 1,498 | 1,498 | 0 | 1.45 s | 1,499 / 1,499 | 44.5 s |

The extra receipt in each run is the first publish accepted after the
promotion.

## Findings

- **No acknowledged event was lost.** On one host the standby's replication
  lag is milliseconds, so this run shows the mechanism, not the production
  window. The production window is the standby lag between the real hosts,
  which `FaasPostgresStandbyLagHigh` watches. The GA gate documents it as a
  window of seconds.
- **The new primary accepted publishes about 1.1–1.5 s after the crash.**
  That covers the database promotion only; a real control-plane failover
  moves the daemons as well (RTO 15–30 minutes, `ControlPlaneFailover.md`).
- **Routing resumed on the new primary with nothing stranded:** every
  receipt settled, the ones written just before the crash included.
- **Routing a 1,500-event backlog took 41–57 s, about 26–36 routes/s,** on a
  busy 2 vCPU host. The drill runs single sweeps in a loop, without #5836's
  backlog reruns.

## Files

- `run-N.json`: the drill report of run N.
- `run-N-load.txt`: `/proc/loadavg` when run N started.
- `commit.txt`: the commit the drill was built from.
