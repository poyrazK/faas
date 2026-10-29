# ADR-20261029180000: meterd retries a rolled minute's record, and billing failures alert

- **Status:** accepted
- **Date:** 2026-10-29
- **Owner:** billing maintainers
- **Amends:** ADR-790 (decisions 3 and 6), ADR-566 (price capture cadence)
- **Related:** ADR-1162 (delivery finality), ADR-20261029094512 (subscription reconciliation)

## Context

The meterd GA review on 2026-10-29 found that one transient error could hold
back usage delivery for every account for up to a day.

ADR-790 decision 3 rolls a closed minute at most once per process, even when
writing its compute-complete record fails, so that a persistent failure cannot
turn every tick into a 15-minute replay. ADR-1162 then made the provider
pusher deliver only the hours before the oldest minute of the 24 h catch-up
window that is not recorded complete. Together they leave a gap: a tick that
rolls its newest minute but then fails price capture, the job sampler or the
record write stores that minute as incomplete, and no later tick in the same
process records it. The pusher stops at that hour for every account until the
minute is 24 h old or meterd restarts. A unit reproduction on main 815424b650
showed delivery still stuck after 150 healthy ticks. The same happens when the
app and job samplers roll a minute in different ticks, because only minutes
both caught up in one tick were recorded.

Production had not hit it (2026-10-29: every minute of 30 days recorded, 7
accounts), but price capture made one snapshot read per account per minute,
so the chance grows with the account count. Nothing alerted on failing sample,
push or dunning ticks or on failed Polar webhook answers; only
`BillingDrift` would notice a stall, after an hour. A Polar organization that
allows several subscriptions per customer was the only thing preventing a
customer from being billed twice after two checkouts, and nothing checked it.

## Decision

1. **Retry the record, not the roll.** A minute both samplers rolled in full
   in this process is recorded compute-complete on the first tick whose price
   capture succeeds, oldest first, at most `MeterCatchUpMinutesPerTick + 1`
   per tick. A failed record write is retried on the next tick. The minute is
   still never rolled twice in one process (ADR-790 decision 3 stands), so a
   persistent price or record failure costs a few upserts per tick, not
   replays. Price capture still gates completeness, now for caught-up minutes
   too. The newest minute keeps an observed row (`compute_complete = false`)
   until it is recorded.
2. **Measure the stall.** `meterd_usage_final_lag_seconds` is how far the
   sample tick is past the oldest minute of the catch-up window not recorded
   complete, read from the range catch-up already reads. Healthy ticks read
   60-120 s.
3. **Alert on the billing pipeline.** Warn-level, `public_status=internal`:
   `FaasMeterdUsageDeliveryStalled` (lag > 30 min for 30 min),
   `FaasMeterdSampleFailing`, `FaasMeterdBillingPushFailing`,
   `FaasMeterdDunningFailing`, `FaasPolarWebhookFailing` (three non-2xx
   answers in 30 minutes, well before Polar disables the endpoint at ten),
   `FaasBillingReconcileFailing` and `FaasBillingReconcileCorrected`. apid
   counts every Polar webhook answer as
   `apid_ops_total{op="polar_webhook",code=ok|rejected|unavailable}`.
4. **One subscription per customer is checked.** The Polar catalog preflight
   (apid boot, meterd start) reads the token's organization and refuses to
   start unless it sees exactly one organization with
   `subscription_settings.allow_multiple_subscriptions = false`.
5. **Price capture reads only on change.** meterd remembers, per account, the
   month and terms it last found stored, and skips the snapshot read while
   they are unchanged. A restart, a plan change, a provider policy change or
   a new month reads again. ADR-566's snapshots are unchanged.
6. **Paid plans start with a spend cap.** `api.DefaultOverageCapAllowances`
   is one allowance: the managed PostgreSQL overage ceiling of twice the
   allowance (`ManagedPostgresOverageCeilingMultiplier`, ADR-20261025094915),
   applied to compute at the overage rate. That is Hobby $2, Pro $10 and
   Scale $30 (`Plan.DefaultOverageCapCents`). `UpdateAccountPlan`, which every
   plan change takes, moves the cap with the plan in the same transaction:
   - Free to paid with no cap sets the new plan's default.
   - A cap equal to the old plan's default becomes the new plan's default,
     or is removed on Free.
   - Any other cap, and a cap cleared on a paid plan, is the customer's and
     is kept.

   Accounts already on a paid plan keep their cap (none) until their next
   move from Free; prod has no billed accounts yet.

## Consequences

- A transient failure delays a minute's record by one tick; delivery and cost
  coverage no longer stall for a day.
- A persistent price capture failure, for example an account with a plan
  meterd does not know, still blocks completeness for every account. It now
  alerts within an hour instead of showing up as drift, and its backlog is
  recorded as soon as capture succeeds.
- A new environment fires `FaasMeterdUsageDeliveryStalled` during its first
  catch-up (about 100 minutes); the runbook says so.
- Starting billing now needs the Polar organization setting turned off;
  sandbox already has it off.
- Steady-state price capture drops from one query per account per minute to
  none.
- A paid customer whose overage passes one allowance has workloads parked
  (the existing cap behavior: 402 on wake, queued jobs wait) until they raise
  or clear the cap or the month ends. A runaway loop or a stolen card costs
  at most one allowance by default. A cap that only matches a default by
  coincidence also follows the plan.

## Rejected alternatives

- **Let failed minutes roll again.** Simple, but it reopens the replay
  ADR-790 decision 3 rejected, and a roll costs far more than a record.
- **Drop price capture from completeness.** Cost reports rely on a price at
  every complete minute; keeping the gate and retrying is as fast.
- **Deliver past incomplete minutes.** ADR-1162 finality exists so a late
  catch-up row never lands in an hour already delivered.
- **Default cap equal to the plan price ($10/$30/$99).** No existing limit
  supports it; the managed PostgreSQL ceiling is the repo's only overage
  default, and one shape for both products is easier to explain.
- **Treat a missing cap as the default.** A customer could no longer remove
  the cap without a new API value, and every existing account would change
  at deploy.
