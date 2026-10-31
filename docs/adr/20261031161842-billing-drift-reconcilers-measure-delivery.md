# ADR-20261031161842: The billing drift checks measure delivery, cover organizations, and a cap excuses only what it withheld

- **Status:** accepted
- **Date:** 2026-10-31
- **Owner:** billing maintainers
- **Amends:** ADR-049 §B.1 (the provider drift check's local figure), ADR-20261019193600 (the ledger dual run's capped result), ADR-1162 (consequence: recent undelivered hours read as drift)
- **Related:** ADR-1162, ADR-20261031100000, #6029 (organization billing subjects)

## Context

A code review of meterd's billing checks on main 5d037cfc2c (2026-10-31)
found three gaps in the two drift checks, and one boundary to state.

1. **The provider drift check paged on correct billing.** It compared the
   provider's total for the last 24 complete hours with the subject's
   overage computed from usage. The pusher rightly delivers less than that:
   nothing above the spend cap, nothing for an hour not yet final
   (ADR-1162), nothing for a suspended or deletion-pending subject. Every
   paid plan now starts with a spend cap, so any account at its cap read a
   drift ratio near 1. The check runs every 6 hours and its gauge holds the
   last value, so one reading trips `BillingDriftAccount` (5 % for 15 min)
   and `BillingDrift` (0.5 % for 1 h).
2. **The ledger dual run excused every shortfall of a capped subject.** It
   classified any delivery below the ledger's net compute as `capped` when
   the subject had a cap at all. With default caps, a lost or short receipt
   on almost any paid subject read as `capped`, never `drift`.
3. **Neither check covered company organizations.** Since #6029 an
   organization that owns resources is a billing subject: the pusher lists
   it (`state.ListBillingResourceAccounts`), but both checks listed
   personal accounts only.
4. **A subject that ends its subscription loses its undelivered paid
   hours.** The subscription-end webhook moves it to Free, and neither the
   pending scan nor the pusher delivers a Free subject's hours
   (ADR-20261031100000 notes this). In steady state that is under an hour
   plus a sampling tick of overage; behind a stalled minute, up to
   `api.MeterUsageFinalAfter`.

## Decision

1. **The provider check compares the provider with delivery receipts.** For
   each paid billing subject it sums the compute MB-seconds the pusher
   recorded as delivered (`billing_usage_deliveries`) over the whole hours
   of the window from the subject's `billing_from`, and compares the
   provider's total over the same hours. Withheld usage has no receipt and
   no provider event, so it is not drift; an event the pusher recorded but
   the provider lacks, or one the provider counts twice, is. Whether the
   receipts are right is the ledger dual run's question. Gauge names, labels
   and alert thresholds are unchanged.
2. **A cap excuses a shortfall only when the pusher could not have
   delivered more.** The pusher clamps each hour to the cap less the month's
   live egress overage and object-storage charge at that hour; both only
   grow within a month, so the allowance at the window's last hour is the
   least it ever granted. The dual run computes the window's net compute
   under that allowance with the pusher's own clamp (`computeWithinCap`),
   counting the month's net compute before the window as already used.
   Delivery at or above that floor is `capped`; below it is `drift`.
3. **Both checks list billing subjects with
   `state.ListBillingResourceAccounts`**, the pusher's own list.
4. **Undelivered paid hours at subscription end stay unbilled.** The
   provider bills metered usage through the subscription, and once it has
   ended there is nothing to attach a late event to. Delivering them first
   would need apid to wait on meterd's pusher before applying the webhook, a
   call across the component boundary, for under an hour of overage in
   steady state. The bound is accepted and stated here.
5. **One implementation of billed compute.**
   `billing.OverageMBSecondsForRange` (invoice credits, object-storage cap)
   is the month's `state.MonthBilledComputeMBSeconds` at the range's end
   less at its start, instead of a second segmented walk over plan history,
   so the cap gate, the pusher's clamps and these callers cannot disagree.

## Consequences

- `BillingDrift` and `BillingDriftAccount` no longer fire for subjects at
  their cap, for hours not yet delivered, or for suspended subjects. They
  fire when the provider and meterd's receipts disagree.
- ADR-1162's consequence "the drift reconciler can report recent undelivered
  hours as drift" no longer holds; usage the pusher has not reached shows in
  the dual run as `pending`.
- A capped subject whose receipts fall short of what its cap allowed now
  counts as `drift` in `meterd_financial_ledger_reconcile_accounts`.
- Organization subjects appear in `meterd_billing_drift_*{account_id}` (the
  label carries the organization ID) and in the dual run's counts.
- The dual run reads the subject's live egress and object-storage charge once
  per drifted-low subject.

## Rejected alternatives

- **Keep comparing usage, but clamp it to the cap and the final hours.**
  Re-derives the pusher's arithmetic in a second place, which is what let
  the two checks drift from the pusher in the first place; receipts already
  record its result.
- **Excuse a shortfall when the month's charges reach the cap.** Needs the
  delivered egress and object-storage charges as well, and a subject well
  under its cap would still hide behind a cap reached later in the month.
- **Flush a subject's pending hours before applying a subscription end.**
  See decision 4.
