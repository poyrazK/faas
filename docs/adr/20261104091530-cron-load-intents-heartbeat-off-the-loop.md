# ADR 20261104091530 · Cron sweep, recent load, operator intents and heartbeat off the loop; a streamed load scrape

- **Status:** proposed.
- **Date:** 2026-11-04
- **Amends:** ADR-191 (the bounded off-loop work pool), ADR-160 (scale-up
  metrics ingestion is bounded and fails closed)
- **Related:** ADR 20261103195512 (relayed failures, jobs reaper and run-now
  drains off the loop), issue #171 (the recent-load mirror)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

A tenth review of `pkg/sched` found four kinds of work still running on
schedd's select loop, plus a scrape that fails at density:

1. **The cron sweep.** At the top of every minute the loop handled each due
   cron (plan, capability, spend-cap and budget checks, claim, enqueue,
   audit, mark), several Postgres round trips per cron, one after another.
   The sweep yielded only after a 10 s slice, so a node with many crons held
   the stats tick, the triggers and notifications for seconds every minute.
2. **The recent-load mirror's scrape.** Every second the loop fetched the
   gateway's `/metrics/gateway-requests` (2 s timeout) and parsed it.
3. **Operator intents.** Each drain processed every pending intent inline:
   a force park waits up to 90 s on vmmd, a restart up to 20 s, a node
   intent up to 30 s of persistence retries.
4. **The heartbeat.** It pings the node with a 5 s timeout and pings a
   recovering node again, so a failing vmmd held the loop about 10 s of every
   30 s.

The scrape also had a ceiling. The exposition holds one row per app per
exact status code, and the gateway never drops an app's rows. The scraper
read at most 1 MiB and failed closed above it, so on a dense or long-running
node both the RPS scale-up trigger and the recent-load mirror (scale-in, the
aggressive reaper) lost their signal together.

## Decision

1. **The four run on the work pool:** `cron_sweep`, `recent_load`,
   `operator_intents` and `heartbeat`, one at a time each.
   - Cron: a tick or resume that arrives during a pass runs once after it,
     so the minute's tried set keeps one writer. The 10 s slice stays as the
     bound on one pass but no longer holds the loop.
   - Operator intents: still processed one after another. A notification
     during a drain asks for one more pass.
   - Recent load and the heartbeat: a tick during a pass is dropped; the
     next tick does the work again.
   - The startup drains stay inline, as before.
2. **The scrape is parsed as it streams.** Memory follows the number of
   apps, not the body. The cap rises to 32 MiB, and one line may be at most
   64 KiB. An oversized or malformed response still fails closed.
3. **The gateway drops a deleted app's request counters** on `app_delete`.
   The in-flight gauge stays, so a request still finishing cannot drive it
   below zero.

## Consequences

- The select loop no longer waits on cron dispatch, the load scrape,
  operator VM work or node probes.
- An operator intent, a cron pass or a heartbeat can run while the loop
  handles other work. Each already relied on durable claims and
  conditional writes, not on the loop for exclusion. The recent-load mirror
  and the heartbeat hold their own locks.
- Scale-up and scale-in keep their signal past 1 MiB of exposition. Deleted
  apps no longer add to it; apps that stay but go quiet still keep their
  rows.
