# V12 against the event GA target, provisional re-run (2026-11-05)

A re-run of V12 against the GA gate's 50 events/s target
([ADR 20261029093000](../../../adr/20261029093000-event-ga-gate.md)) on main a
week after the two 2026-10-29 runs: [before](../20261029-event-ga-v12/REPORT.md)
and [after](../20261029-event-ga-v12-counts/REPORT.md) #5883 made
`EventDeliveryCounts` read the account's live slots alone. Same script and
host. It checks that the result still holds on current main.

- **Host:** `gregale-internal-test-1` (2 vCPU, PostgreSQL 16.15, shared with
  other jobs). The host was quiet: a 3-minute wait, 1-minute load 0.15–3.5 at
  the start of each run.
- **Build:** `82efbc89` (main, 2026-11-05).
- **Status:** provisional. Promotion still needs a run on the OVH reference
  host.

## Result: the target is met on this host

| Run | 1-min load at start | 20/s | 35/s | **50/s** | 65/s | 80/s | 100/s |
|---|---:|---|---|---|---|---|---|
| `sweep-1` | 0.71 | 100 %, p95 0.05 s | 100 %, p95 0.05 s | **99.8 %, p95 0.22 s ✅** | 95 %, p95 1.1 s | 55 % | 33 % |
| `sweep-2` | 3.51 | 100 %, p95 0.06 s | 100 %, p95 0.06 s | **99.8 %, p95 0.27 s ✅** | 99 %, p95 0.33 s | 66 % | 39 % |
| `standard` (2,000-event stages first) | 0.15 | — | — | **99.8 %, p95 0.43 s ✅** | — | — | — |
| `ordered` (20 keys; target 20/s) | 3.45 | **100 %, p95 0.04 s ✅** | — | 99.2 %, p95 0.63 s | — | — | — |

A rate passes when ≥ 95 % of the offered rate is achieved, nothing is pending
after the 60 s settle window, and p95 publish-to-handler is ≤ 5 s. Every run
here settled with nothing pending.

Against 2026-10-29:

| Run at 50/s | 10-29, before #5883 | 10-29, with #5883 | 2026-11-05 |
|---|---|---|---|
| `standard` | 49 %, p95 17.8 s ❌ | 98.7 %, p95 0.42 s ✅ | 99.8 %, p95 0.43 s ✅ |
| `sweep-1` | 89 %, p95 2.1 s ❌ | 99.9 %, p95 0.18 s ✅ | 99.8 %, p95 0.22 s ✅ |
| `sweep-2` | 99.8 %, p95 0.34 s ✅ | 99.9 %, p95 0.16 s ✅ | 99.8 %, p95 0.27 s ✅ |
| `ordered` | 45 % | 79.5 % | 99.2 %, p95 0.63 s |

The #5883 result holds on current main: the capacity knee stays near 65/s,
and the `standard` run's 2,000-event backlog no longer slows routing.
Ordered lanes at 50/s, which is not a target, went from 79.5 % to 99.2 %.

## Where the time went

| Statement | 10-29, before #5883 | 2026-11-05 |
|---|---:|---:|
| `EventDeliveryCounts`, per call | 4.8 ms + 2.4 s planning | 0.06 ms |
| `EventStorageAppend`, per publish | 5.0–6.3 ms | 3.3 ms |
| outbox insert trigger, per publish | 2.3–3.0 ms | 1.5 ms |
| `EventStorageLockAccount`, per publish | 1.4–1.6 ms | 1.0 ms |
| `refresh_event_routing_backlog`, per publish | about 3 ms | about 2 ms |
| Top-level database time, `standard` run | 204 s | 122 s |

The publish path now costs about 6 ms of database time per event, down from 9.
The remaining per-delivery cost is the invocation claim (3.1 ms) and the
lifecycle update (1.8 ms).

## Finding: one account's webhook backlog drained at 18 deliveries/s

`standard`'s webhook stage delivered 18.2/s, against 122/s and 159/s in the
2026-10-29 runs with the same settings: one account, 64 subscriptions, a 0 ms
receiver. #6367 landed between them (2026-11-03). The
account in-flight cap (16, ADR-20261101093517, #6367) was correct, but the
dispatcher read a claim the cap had emptied as "no backlog" and waited for the
next 5 s tick. Claim logging showed bursts of 16-delivery claims, then an
empty claim, then a 4–5 s gap. A local PostgreSQL run reproduced it (11.6/s),
and lifting the cap restored 121.7/s. The fix, in the same change as this
report, keeps the backlog after an empty claim while the dispatcher still has
deliveries running. On the same local PostgreSQL the bench went from 11.6
to 153.1 deliveries/s with it.

## Files

- `<run>-event-pipeline.json`, `standard-webhook-delivery.json`: the
  benchmark's reports.
- `<run>-statements.tsv`: top statements by total time during the run.
- `<run>-load-start.txt`, `<run>-window.txt`: host load and the run's UTC
  window.
- `commit.txt`: the commit measured.
