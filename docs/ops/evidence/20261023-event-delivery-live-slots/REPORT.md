# Live event delivery slots — 2026-10-23

`perf/event-delivery-live-slots` gives each `event_delivery_slots` row a `live`
flag, kept by triggers, and counts an account's live deliveries from its live
slots through the partial index `event_delivery_slots_live`, with joint
statistics on `(account_id, live)`. ADR-614 delivery limits run this count on
every event admission and replay.

Measured on `gregale-internal-test-1` (2 vCPU, PostgreSQL 16.15, a throwaway
cluster), 2026-10-23.

## The count against a growing history

`count_bench_live.py` loads the branch's `schema.sql` into a disposable
database, seeds an account with 50 live deliveries and a growing retained
history, plus another account with 200,000 retained deliveries and a live
backlog, and times the count as main runs it (every slot of the account) and
as the branch runs it (live slots). Both run as prepared statements, warmed
before nine `EXPLAIN (ANALYZE, BUFFERS)` runs; medians.

`count-fresh.jsonl` and `count-stale.jsonl`, before the joint statistics:

| Statistics | Retained slots | Live elsewhere | Main | Branch |
|---|---|---|---|---|
| fresh | 0 | 50 | 0.065 ms | 0.119 ms |
| fresh | 10,000 | 50 | 0.215 ms | 0.117 ms |
| fresh | 500,000 | 50 | 0.210 ms | 0.129 ms |
| fresh | 500,000 | 10,000 | 111.3 ms, 7,683 buffers | 7.7 ms, 203 buffers |
| fresh | 500,000 | 50,000 | 135.2 ms, 9,016 buffers | 10.1 ms, 1,160 buffers |
| analyzed while empty | 2,000 | 2,000 | 1.03 ms | 0.54 ms |
| analyzed while empty | 100,000 | 2,000 | 17.0 ms | 0.53 ms |

`count-fresh-statistics.jsonl` repeats the fresh rows and then adds the joint
statistics. It ran while other tests used the host, so all its times are two to
three times higher; compare within the run:

| Retained slots | Live elsewhere | Main | Branch, column statistics | Branch, joint statistics |
|---|---|---|---|---|
| 500,000 | 50,000 | 438.4 ms | 59.4 ms | **0.24 ms**, 203 buffers |
| 500,000 | 60,000 | 426.4 ms | | **0.34 ms**, 203 buffers |

With column statistics alone the planner multiplies the account's share of
slots by the live share, expects tens of thousands of live slots for an
account that has 50, and reads every live invocation on the platform. Joint
statistics let it read the account's live slots.

## V12

Main `885ad682` and the branch at `23a821e6` (before the joint statistics),
standard runs and offered-rate sweeps (`event-pipeline*.json`,
`statements.tsv`). The host carried other load throughout: 1-minute load
median 3.9 and 4.5 for the standard runs, 6.4 and 7.7 for the sweeps. Statements
the branch does not touch slowed by similar margins in its runs
(`EventStorageAppend` +46%, the webhook delivery update +78% in database
time), so the runs do not separate the change from
the load. The benchmark also starts with no delivery history, so it cannot
show the change's benefit. What it does show is the triggers' cost: about
0.08 ms per event.

## Findings

1. **The admission count no longer reads the account's delivery history.**
   With a backlog elsewhere it fell from 111–135 ms to under 0.4 ms, and from
   17 ms to 0.5 ms with statistics taken while the tables were empty, which
   is how they start.
2. The joint statistics are load-bearing: without them the count still reads
   the platform's live deliveries once those outnumber the account's.
3. With no history the count is slower (0.12 against 0.07 ms): it reads the
   live index instead of the live invocations.
