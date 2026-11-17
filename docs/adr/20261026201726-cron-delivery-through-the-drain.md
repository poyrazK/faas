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

## Amendment 2026-10-29: retry runs Gregale refused before delivery

Production on rc.304 showed plain HTTP cron runs failing on their single
attempt although the app never received the request: the gateway's dispatch
endpoint answered `503 traffic_policy_unavailable` ("Retry shortly"), and
before rc.304 the traffic-security version pin and the wake path timed out
reading generations. One attempt was meant to keep a cron from running its
handler twice, which a refusal before delivery cannot do.

A plain HTTP cron run now gets up to `api.CronPlatformAttempts` (3) attempts
when Gregale refuses it before the app sees it, waiting
`api.CronPlatformRetryDelay` (5 s) times the attempt number between them:

- a wake failure other than a permanent one;
- a traffic-security version-pin failure (dispatch not started);
- a 5xx from gatewayd-internal's dispatch endpoint (`sched.ErrDispatchRefused`).
  The endpoint answers 200 with the app's response whenever the app received
  the request, so a 5xx is a refusal before delivery.

Anything the app answered, including its own 5xx inside the 200 envelope,
and a dispatch that timed out in flight keep the single attempt. The last
refused attempt ends `failed`, not dead-lettered. Crons with failure rules
are unchanged: they already use the plan's retry budget.

## Amendment 2026-11-02: one attempt for every cron without failure rules

A cron with a schedule policy (ADR-385) but no failure rules queued its run
without the single-attempt policy, so the drain gave it the plan's queue
retry budget: an app answering 5xx or timing out received the same scheduled
time up to 3 (Hobby), 10 (Pro) or 25 (Scale) times, then the run was
dead-lettered. ADR-385 keeps the retry behavior of definitions without
failure rules, which for an HTTP cron is one delivery. Its runs now carry the
single-attempt policy and the platform-refusal retries of this ADR's
2026-10-29 amendment, as plain crons do; a cron with failure rules keeps the
plan budget its rules may use.

A plain cron whose run could not be queued (a store read or the enqueue
failed) consumed its claimed occurrence: the claim was not rolled back and the
run was lost, with only the audit row recording it. Such a failure now rolls
the claim back, so the next minute's sweep queues the run. An enqueue the
store refuses for good still consumes the occurrence, so a cron cannot retry
it every minute.

A cron with a schedule policy on an app with no live deployment is suspended
(`no_live_deployment`), as a plain cron is, instead of queuing a failing run
every tick.

The cron sweep runs on schedd's main loop, which also beats the node's
heartbeat. It now dispatches for at most 10 s per pass and continues in
further passes within the minute, so a minute with many due crons cannot hold
the heartbeat past its 90 s staleness.

## Amendment 2026-11-02 (2): held apps, removed crons, interrupted manual runs

A strict compute budget that holds an app makes the drain defer its runs a
minute at a time without spending them. The cron sweep kept firing, so a cron
without `skip_if_running` queued a run every scheduled time of the hold and the
backlog was delivered at once when the hold lifted. The sweep now leaves the
cron of a held app due, as for a scheduled time Gregale could not reach: it
runs once after the hold.

Only `DeleteCron` cancelled a removed cron's runs. A cron removed by a deploy's
`gregale.yaml`, a project apply or an app's deletion left them queued, and
`invocations.cron_id` is `ON DELETE SET NULL`, so they reached the app with no
cron to show them. Every cron run is queued with its cron's id, so the drain
now cancels a cron run whose id is NULL or names a cron that is gone.

Claiming a manual run's request and linking it to the run it queued are
separate writes. A scheduler that stopped between them left the request
running forever. The status writes of a request being processed now outlive a
stopping scheduler, and at start and on each safety tick schedd fails its
node's running requests older than two minutes that never linked a run, with a
reason that says the run may already have been queued.


## Amendment 2026-11-03: company members, staged crons, dashboard runs

`GET /v1/crons?slug=` found only an app the caller created, so a company
app's other members got 404 from `gregale crons list --app` and from the
`gregale.yaml` deploy, which lists the app's crons first. The slug now
resolves through the same app authorization as the cron writes.

Deployment cron staging (`POST /v1/apps/{slug}/deployment-cron-staging`)
checked only a leading `/` and a loadable timezone. A `stage` or `prepare`
now applies the cron API's path rules (`api.ValidateCronPath`) and refuses the
host-dependent `Local` zone for every cron it adds or changes. A cron carried
with its stored path and zone, and a `restore` or `cancel` of an intent
already staged, keep the rules they were written under.

The dashboard's manual run refuses a disabled cron before queuing a request,
as the scheduler refuses its run, and names each refusal (disabled, too many
manual runs waiting, suspended, over the plan) instead of a generic failure.

## Amendment 2026-11-03 (2): removable policies, held manual runs

A cron's schedule policy and failure rules could be set but never removed:
`PATCH /v1/crons/{id}` keeps an omitted field, and the store wrote only a
value that was present. `clear_schedule_policy` and `clear_failure_rules`
(`gregale cron update --clear-schedule-policy`, `--clear-failure-rules`)
remove them; each is refused alongside the field it clears. The Terraform
`gregale_cron` plans null for either when the configuration drops it and
sends the clear, where it planned no change before.

A compute budget holds only preview apps. On one, the sweep leaves crons due
(amendment 2026-11-02 (2)), but `gregale cron status` reported the cron
firing on schedule and a manual run was accepted, then deferred by the drain
until the hold lifted. Status now reports `budget_held`, and a manual run is
refused with `402 financial_budget_held`. A manual run of a disabled cron is
refused with `409` instead of failing in the scheduler a moment later. The
scheduler's PostgreSQL cron listing drops disabled crons, as MemStore did.

## Amendment 2026-11-05 (2): stopped crons' queued runs, removing long-lived crons

The drain dropped a queued cron run only when its cron was gone. A disabled
or suspended cron's runs still reached the app: those the spend cap or a
budget hold deferred (delivered when the hold lifted, after the customer had
switched the cron off), retries under failure rules or after a platform
refusal, and manual runs still queued. A run kept the path it was queued
with, so a path fixed after a 404 kept being called at the old one. The drain
now cancels a cron run whose cron is disabled or suspended and delivers the
others to the cron's current path. This reverses the earlier documented "disabling
does not cancel a queued run": disable is the customer's way to stop a cron.

A deploy's `gregale.yaml`, its activation and a project apply removed crons
with one `DELETE` inside their transaction. For a long-lived cron the
foreign key's `SET NULL` detached every run inline, the failure `DeleteCron`
avoids by detaching in committed batches (hunt #9, H9-165): the statement held
the account's cron quota lock until it timed out, and the activation worker
failed every pass. A removal now checks how many runs its crons hold. Past
1,000 it rolls back, detaches the settled runs in committed batches and tries
again (three attempts). Inside the transaction it cancels the crons' runs that
have not started, so an aborted removal never loses one. An aborted removal can
leave a surviving cron's settled runs out of its history. Schedule occurrences
still cascade inline; a schedule-policy cron's 90 days of them is a known
residual.
