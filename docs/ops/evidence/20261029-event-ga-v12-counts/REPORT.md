# V12 against the event GA target, with the delivery-count fix (2026-10-29)

The same protocol and host as [20261029-event-ga-v12](../20261029-event-ga-v12/REPORT.md):

- **Host:** `gregale-internal-test-1`, 2 vCPU, shared. Measurement started
  after an 11-minute wait for a quiet host.
- **Build:** `f28b0beb`, the earlier build plus #5883. #5883 makes
  `EventDeliveryCounts` read the account's live slots alone (ADR-614
  amendment 2026-10-29).
- **Status:** provisional. Promotion still needs a run on the OVH
  reference host.

## Result: the 50 events/s target is met in every run

| Run | 1-min load at start | **50/s** | 65/s | 80/s | Before the fix (50/s) |
|---|---:|---|---|---|---|
| `standard` (2,000-event stages first) | 1.93 | **98.7 %, p95 0.42 s ✅** | — | — | 49 %, p95 17.8 s |
| `sweep-1` | 1.84 | **99.9 %, p95 0.18 s ✅** | 97.3 %, p95 0.75 s | 57 % | 89 %, p95 2.1 s |
| `sweep-2` | 2.94 | **99.9 %, p95 0.16 s ✅** | 98.4 %, p95 0.50 s | 66 % | 99.8 %, p95 0.34 s |
| `ordered` (20 keys; target 20/s) | 2.06 | 79.5 % (not a target) | — | — | 45 % |

The ordered target holds: 100 % at 20/s, p95 0.04 s. Hosts were somewhat
quieter than in the first run, but `standard` started at a similar load
(1.93 against 1.60) and went from 49 % to 98.7 %.

| Stage | Before | After |
|---|---:|---|
| Fanout over the 2,000-event backlog | 39/s | **104/s** |
| Drain | 96/s | 132/s |
| Publish | 425/s | 543/s |

## Where the time went

`EventDeliveryCounts` is now **0.04 ms** a call, from 4.8 ms
(`standard-statements.tsv`: 205 ms over 5,100 calls), plus about 0.28 ms of
planning. The rest of the event path is unchanged:

| Statement | Per call |
|---|---:|
| `EventStorageAppend` | 3.5 ms |
| invocation claim | 3.8 ms |
| outbox insert trigger | 1.6 ms |
| backlog projection refreshes | about 1 ms per event |
| `EventStorageLockAccount` | 1.1 ms |

These are the levers for more headroom: the projection and the triggers on
`invocations`.

## Files

As in the first run: `<run>-event-pipeline.json`,
`standard-webhook-delivery.json`, `<run>-statements.tsv`,
`<run>-load-start.txt`, `<run>-window.txt` and `commit.txt`.
