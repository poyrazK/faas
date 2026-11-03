# ADR-1162: Billing delivery finality, a hard spend cap, and plan-history allowances

Status: accepted (local implementation; no live provider qualification yet).

Amends spec §4.7, §4.7.1 and §10, and the issue #561 spend-cap behaviour.

## Context

A billing review found four ways recorded usage and provider charges could
diverge:

1. **Late usage after delivery.** The provider pusher delivered any completed
   UTC hour that had usage. The sampler re-rolls closed minutes it never
   recorded compute-complete for up to `MeterCatchUpWindow` (24 h, 15 minutes
   per tick). A provider usage event is keyed by `(account, hour)` and a
   delivery receipt is first-write-wins, so rows written after an hour was
   delivered were never billed. Every meterd restart or failed tick leaked.
2. **The spend cap was free compute.** At the cap, schedd refused request
   wakes, but live instances, min-instance floors, service replicas, workers,
   warm pools and jobs kept running: explicit-deployment admissions bypass the
   request gate, and nothing parked the account. The pusher billed nothing for
   the capped hour and left it pending, so raising the cap later back-billed
   up to 30 days of usage the customer had been told was capped.
3. **App purge erased billing evidence.** `DeleteAppPermanently` runs 7 days
   after an app delete (`AppDeleteGraceDays`) and deleted the app's
   `usage_minutes` and `usage_daily`. Month-to-date sums drive the included
   allowance, overage deliveries, the cap, invoice credits and the reconciler,
   so a mid-month purge handed the deleted app's usage back as allowance.
   Rotating apps roughly doubled the allowance and reset the Free hard stop.
4. **Current plan applied to the whole month.** Every overage computation used
   the account's current plan. Plan changes land mid-month (Polar applies a
   paid-to-paid change at the subscription anniversary), so a downgrade shrank
   the month's allowance retroactively and every later hour was billed in full.

## Decision

1. **Deliver only final hours.** A minute is final once it is recorded
   compute-complete (`financial_sampling_windows`) or is older than
   `api.MeterUsageFinalAfter` (the catch-up window plus the sampler's
   newest-closed-minute offset and one tick of skew). `PushPending` delivers
   an hour only when every minute up to its end is final. Delivery is never
   lost, only delayed: a minute that is never recorded complete holds
   delivery for at most `MeterUsageFinalAfter`. The egress meter uses the
   same boundary. `PushHour` remains the unguarded single-window test seam.

2. **The spend cap is a hard monthly ceiling.**
   - *Billing.* An hour that crosses the cap is billed exactly up to it and
     settled; an hour entirely above it is settled with a zero receipt. Capped
     usage is never billed, even if the cap is raised later. Compute reserves
     live egress overage and egress reserves billable compute, with exact
     rational arithmetic (`capComputeAllowance`, `capEgressAllowance`). Each
     meter reserves the other's month-to-date *billable* cost rather than its
     delivered cost, because the delivered cost can exceed a later, smaller
     allowance; when both meters are clamped the cap can be under-used, never
     exceeded.
   - *Runtime.* schedd parks every app of a capped account through the
     reaper's lifecycle reconcile (`Engine.appMustPark`, the same path as a
     suspended or abuse-held account). Explicit-deployment admissions (floors,
     service replicas, workers, per-deployment wakes) now check the cap;
     deployment smoke and mirror instances stay exempt. Warm pools are
     reclaimed and not refilled. Queued job tasks are deferred, not failed, by
     `SpendCapJobDeferInterval`. Raising or clearing the cap, or the month
     rolling over, lifts every hold through the existing 5 s overage cache.
     The cap check fails open on read errors, as before.

3. **App purge keeps usage.** `DeleteAppPermanently` no longer deletes
   `usage_minutes` or `usage_daily`. Neither has an FK to `apps`; the 13-month
   usage retention cron and account deletion remove them.

4. **The month's allowance follows its plan history.** The allowance for usage
   up to an instant is the largest compute grant among the month's recorded
   price activations (`financial_price_snapshots`) that precede it
   (`state.MonthComputeIncludedMBSeconds`). A downgrade never shrinks the
   month's allowance, matching the financial ledger's "largest recorded grant"
   rule; an upgrade applies from its recorded activation. Before the first
   activation the earliest applies; with none, the current plan.
   `CurrentMonthOverageCents` (spend-cap gate, quota loop, dashboard) uses the
   same rule and never less than the current plan's allowance, so the gate,
   the dashboard and the provider agree.

## Consequences

- Provider deliveries trail usage by up to one hour plus one sampling tick in
  steady state, and by up to `MeterUsageFinalAfter` behind a stalled minute.
  A sample tick that fails, including its financial price capture, leaves its
  minute incomplete, so a persistent tick failure delays every delivery by
  that bound; it already turns meterd `/healthz` red.
  The drift reconciler can report recent undelivered hours as drift until
  they are delivered.
- A customer at the cap sees workloads parked within one reaper tick and new
  wakes refused with `402 admission_refused`, and is charged at most the cap.
  Gregale absorbs the sub-tick of usage between reaching the cap and parking.
- A cap of 0 already counted as reached at zero overage (issue #561's pause
  switch, pinned in `overage_test.go`). It now also parks running work, so a
  zero cap pauses the whole account, not only its new wakes.
- The sampler keeps writing synthetic min-instance floor rows for floors the
  cap parked. Those hours settle at the cap and are never billed, but they
  appear in usage summaries.
- Usage of purged apps stays in month totals, usage APIs and exports until
  normal retention, attributed to an app ID that no longer resolves.
- Overage billed before a mid-month upgrade is not refunded when the upgrade
  raises the allowance; provider events cannot be negative. The financial
  ledger's month-end view can therefore be lower than what was charged in
  that case.

## Rejected alternatives

- *Adjustment events for already-delivered hours.* Needs per-provider
  correction semantics (Polar dedupes on the hourly external ID; Stripe and
  Paddle differ) and cannot correct a closed provider period. Finality makes
  late rows impossible instead.
- *Spend cap as account suspension.* Reuses the reaper path but changes
  customer-facing status, errors and dunning interactions, and needs a schema
  change for a new suspension reason.
- *Keep holding capped hours.* That is the back-billing this ADR removes.
- *Current plan at push time.* That is the retroactive allowance change this
  ADR removes.

## Not covered

App tasks and environment-qualification runtimes do not check the cap at
admission; they are bounded by their task timeouts. Snapshot storage, builds, CPU and managed Postgres remain unbilled.
The pusher still reads raw usage rows rather than the financial ledger.

## Amendment 2026-11-03: queued work waits at the cap

Status: accepted (cost-control audit 2026-11-03).

Decision 2 deferred queued job tasks at the cap but said nothing about other
queued work. A review found three gaps:

- **Invocations were dead-lettered.** The drain claimed a capped app's
  pending invocations (crons, delayed tasks, webhooks, async invokes, event
  deliveries) and treated the wake gate's 402 as a failed wake. Each retry
  spent an attempt, and the row was dead-lettered with work the customer had
  not lost.
- **Scheduled work piled up.** A cron fired every scheduled time through the
  cap, and a recurring job queued a run every scheduled time. The dispatcher
  defers held work without failing it, so the whole backlog ran when the cap
  lifted, often in the first hours of the next month. Strict budget holds
  queue job runs the same way; the crons audit (2026-11-02) had fixed the
  cron case for strict holds only.
- **The billing page understated the cap.** The cap bar and the admin billing
  view counted compute and egress, while the hold also counts the delivered
  object-storage charge (ADR-20261027093220 decision 4). A page could show
  headroom while the apps were parked.

Decision:

1. The drain checks the billing subject's cap before it claims a row. At the
   cap the row moves back by a minute without spending an attempt, as a
   budget hold does.
2. The cron sweep skips an app whose billing subject is at its cap. The cron
   stays due and runs once after the cap lifts.
3. A due recurring job stays due while its account is at its cap. A job with
   `overlap: allow` (the default) also stays due while an earlier scheduled
   run has not started, so a strict hold or a full account queue holds one
   unstarted run at most. `skip` and `replace` already decide against queued
   runs. The job's `missed_runs` policy records what it missed, as after any
   time the scheduler could not reach it.
4. Every view that compares the month's spending with the cap counts the
   delivered object-storage charge through the hold's own helper
   (`meter.SpendCapObjectStorageCents`). The usage summary's
   `overage_cents` stays compute and egress overage: it is a per-meter view.

Consequences:

- A capped account's crons and recurring jobs run once after the cap lifts,
  not once per missed time. Customers who need every occurrence set a higher
  cap.
- An `overlap: allow` job whose runs queue for longer than its schedule
  interval now has occurrences recorded as missed instead of runs queued
  behind each other.
