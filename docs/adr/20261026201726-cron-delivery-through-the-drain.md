# ADR-20261026201726: Cron delivery through the drain, and schedule anchors

- **Status:** accepted
- **Date:** 2026-10-26
- **Amends:** ADR-090 (cron fire-now), ADR-1180 (long invocations), spec §4.10 (cron)

## Context

The crons GA review on 2026-10-26 found two defects that block a public
release of HTTP crons.

**Delivery held the scheduler loop.** `runCronTick` runs on schedd's main
select loop. For a plain HTTP cron (no schedule policy, request timeout up to
30 s) it woke the app and waited for the response inline, one cron after
another. Manual runs (`POST /v1/crons/{id}/run`) took the same path through
`drainPendingFireNowRequests`, with no limit beyond the API's 401 counter.
The same loop drives the node heartbeat, the idle reaper and the watchdog.
A few slow crons due in the same minute, or one account queuing manual runs
against a slow endpoint, held the loop for minutes: other tenants' crons
fired late, idle instances were not parked, and once the heartbeat went
stale for 90 s a peer's stale-peer observer marked the node unavailable (the
ADR-1344 takeover guard is still off by default). ADR-1180 had already moved
long-timeout apps to the drain for this reason; ordinary apps stayed inline.

**Edits fired at once.** The next fire was the first boundary after
`last_fired_at` under the current schedule. Nothing reset that cursor, so
moving a 03:00 job to 04:00 at 14:00 fired it at 14:00, enabling a cron that
had been off for a week fired it at once, and so did a redeploy that lifted
a `no_live_deployment` suspension. Policy and command crons instead recorded
one missed occurrence per passed boundary, up to 10,000 sequential
transactions per cron, on the same loop.

## Decision

1. **A cron fire is a queued invocation.** The tick and the manual path
   persist a pending `source=cron` invocation and return; the invocation
   drain wakes the app and delivers it with its bounded parallelism,
   per-tenant fairness and per-account in-flight cap. Neither path calls
   the gateway. Before queueing, the tick suspends a cron whose app has no
   live deployment, as the inline wake failure used to. The `cron.fired`
   audit row records `status="queued"` and the invocation id; the instance
   is known only on the invocation.
2. **One delivery attempt per plain HTTP fire.** A cron without failure
   rules queues its invocation with `max_attempts=1`, and the drain ends a
   failed attempt as `failed` (not `dead_letter`, so failed fires stay out
   of the DLQ). The next boundary is the retry, as before. This also
   applies to long-timeout apps, which ADR-1180's handoff retried. A cron
   with failure rules keeps the plan retry budget its rules may use.
3. **Manual runs report the run.** A claimed HTTP fire-now request stays
   `running` with its invocation id; `GetFireNowRequest` reports the
   invocation's terminal state (`succeeded`, `failed` with its error,
   `cancelled`). Reading the outcome rather than writing it back means no
   settle step can race the drain. A cron may have
   `CronFireNowActivePerCron` (5) manual runs pending or running, counted
   over `CronFireNowActiveWindow` (15 minutes) so a run that never settles
   cannot block manual runs for good; another gets
   `429 cron_fire_now_busy`. One sweep claims at most 256 requests.
4. **A schedule anchor restarts the schedule.** `crons.schedule_anchor_at`
   is stamped by the existing `crons` BEFORE UPDATE trigger when the
   schedule or timezone changes, a disabled cron is enabled, or a
   suspension is lifted, so every writer (API, deploy-time cron staging,
   reactivation, app restore) gets it. The next boundary is computed from
   the later of `last_fired_at` (or `created_at` before the first fire) and
   the anchor. `last_fired_at` keeps meaning the last fire. The command-cron
   observer uses the same cursor so its overdue alert agrees with the
   scheduler.

Command crons are not part of the GA surface: they run as app tasks, which
are in preview and off on hosts with `FAAS_APP_TASK_API_ENABLED=0`. The
catalog, the CLI and the new customer page say so.

## Consequences

- The scheduler loop's cron work is database writes only; a slow or hung
  app delays its own runs, not the node.
- A fire now waits for the drain (notified on insert, 1 s safety tick), so
  a run starts within about a second of its minute rather than inside the
  tick. Runs of an account at its async in-flight cap wait for a slot.
- A wake failure is recorded as a failed run with its error. It used to be
  logged and the fire lost without a run.
- Manual-run requests can stay `running` in the table after their
  invocation has finished; readers derive the outcome. A request whose
  invocation was deleted by retention keeps reporting `running`.
- The trigger stamps the anchor whichever release writes the row, since
  the migration lands first (deploy order: data first). Release N-1 schedd
  ignores the column and keeps the old behaviour until it is replaced.
