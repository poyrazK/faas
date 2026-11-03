# ADR: Event routing, receipt pruning and invocation retention keep going while there is a backlog

Date: 2026-10-29
Status: Accepted
Amends: [ADR-191](191-scheduler-divergence-and-bounded-dispatch.md) (one duration sample per off-loop task), [ADR 20261019104819](20261019104819-invocation-default-retention.md) (500 rows a minute), [ADR 20261023215834](20261023215834-event-plan-caps.md) (receipt pruning cadence)

## Context

GA throughput gaps from the 2026-10-25 event audit:

- **Routing.** A fanout sweep routes at most 100 receipts and 100
  recipients, plus up to 100 recovery items and 100 backfill pages, then
  stops without asking for another pass. Only an `event_published` wake or
  the 5 s safety tick starts the next one. Once publishing goes quiet, a
  backlog left by a burst or an outage drains at about 20 routes/s per
  schedd.
- **Receipt pruning.** Each 10 s pass prunes at most 100 settled receipts
  per account. That is about 10/s per account, only 1.45 times Scale's
  6.9 events/s sustained target. After a downgrade or an outage, clearing an
  account's excess takes days, and its publishes get 429s while it is over
  its storage budget.
- **Invocation retention.** Each minute, the reaper, the default-horizon
  stamp, attempt-history pruning and execution-ledger pruning each run one
  batch of 500 rows. That is about 8 rows/s per schedd, below the event
  path's own rate once fanout multiplies invocations, so the tables bloat
  under sustained load. The pass also ran on the loop goroutine.

## Decision

1. **Routing reruns while it has a backlog.** A sweep notes when any part
   of it used its whole budget. The work pool then runs the next pass at
   once, through the existing `rerunCoalesced` path, so the pass is still
   one at a time per schedd and every pass stays bounded. A drained or
   paused sweep reruns only on a wake or the tick, as before.
2. **One duration sample per pass.** The pool records
   `schedd_loop_work_duration_seconds` for each pass, reruns included, not
   once per chain. Otherwise a long drain would look like no completed
   sweep to `FaasEventRoutingStalled` and like one very slow sweep to
   `FaasEventRoutingSweepSlow`. Only `event_fanout` reruns, so no other kind
   changes.
3. **Receipt pruning repeats rounds.** One call keeps selecting due accounts
   and pruning a page of up to 100 receipts from each, round after round,
   until it has pruned its 5,000 limit or a round prunes nothing. Every due
   account still gets a page each round. A sweep that prunes the whole limit
   prunes again on the next sweep instead of waiting out the 10 s interval.
   One account's backlog now drains at up to about 1,000 receipts/s per
   schedd.
4. **Invocation retention repeats full batches, off the loop.** Each
   retention step repeats while its batch of `InvocationRetentionBatch`
   (500) rows comes back full, at most `InvocationRetentionBatchesPerPass`
   (20) times: up to 10,000 rows a minute per step on each schedd. The pass
   runs on its own one-slot work kind, `invocations_retention`, so a long
   pass never holds up the loop's wakes and ticks.

## Consequences

- After a burst or an outage, a schedd routes as fast as its one sweep slot
  allows instead of about 20/s, and accounts over their storage budget get
  back under it in minutes, not days.
- Draining keeps one database connection busy per schedd for as long as the
  backlog lasts. Every statement is still bounded as before.
- The loop-work duration histogram now counts passes. Only `event_fanout`
  reruns, so other kinds are unchanged.
- No schema change. An older schedd keeps its fixed budgets, and both run
  side by side.
