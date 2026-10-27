# ADR 20261027093220 · Object storage is billed daily inside its month

- **Status:** accepted (2026-10-27)
- **Date:** 2026-10-27
- **Amends:** [ADR-1320](1320-object-storage-plan-allowances-ceilings-and-overage-cap.md)
  (object storage under the overage cap) and ADR-156 (month-close object-storage billing)

## Context

Object storage was billed once per UTC month, at month close, and month close
billed only accounts on a paid plan at that moment (Hunt 19 H19-1):

- A customer whose subscription ended during a month moved to Free. Month
  close then wrote a zero `plan_ineligible` receipt, so their last month of
  storage overage was never billed.
- Without that check, the month-close event would still arrive after the
  subscription's last cycle, and the provider would not invoice it.

Compute and egress avoid both problems because meterd delivers them hourly,
inside the subscription's cycle.

## Decision

1. **Daily steps.** apid delivers each paid account's month-to-date
   object-storage charge at most once every `api.ObjectStorageProgressInterval`
   (24 h). The sweep runs hourly.
   - The month-to-date charge prices the usage accrued so far, with no
     requirement that it covers the whole month (`state.SummarizeObjectStorageUsageToDate`).
     That is either the gateway-metered rows or each backend's latest provider
     report.
   - It is priced against the month's allowance, taken from plan history (ADR-1162).
   - Usage only grows, so a step never bills more than the month will cost,
     unless the allowance grows later (a mid-month upgrade). As with compute,
     steps already delivered are not refunded.
2. **Ledger.** `object_storage_billing_progress` holds one row per provider,
   account, month and mode (`shadow` or `live`). Each row records the
   cumulative charge delivered and at most one pending step. Every write is a
   compare-and-set on the row's version, so two apids never deliver the same
   step. A step goes through four writes:
   1. **Reserve.** Set pending to the month-to-date charge minus delivered,
      the most this step can deliver.
   2. **Fix.** Price the step under the cap, then set pending to its real
      delta and mark it fixed. A zero delta releases the reservation instead.
   3. **Publish** the delta (live only). The idempotency key is the account,
      the month and the cumulative target, so after a crash a fixed step is
      published again under the same key.
   4. **Complete.** Add pending to delivered.
3. **Month close.** One last step brings the month up to the record's total,
   held under the cap. Month close then writes the receipt with the cumulative
   delivered quantity, and the receipt stays the period's final word.
   - An account no longer on a paid plan gets no new charge. It only finishes
     a step that was already fixed, and its receipt records what was delivered
     while it was paid.
   - `plan_ineligible` with zero applies only when nothing was delivered.
4. **Cap (amends ADR-1320).** Object storage now takes cap headroom as it is
   billed, not last at month close.
   - A step's cumulative target is `min(month-to-date charge, cap − billable
     compute − billable live egress)`, and never less than what was already
     delivered.
   - meterd reserves the month's live object-storage charge, delivered or
     pending, before it clamps compute and egress.
   - meterd also counts the delivered charge toward the spend-cap hold
     (ADR-20261026120843), so schedd parks compute once the combined total
     reaches the cap. A pending step's reservation is an upper bound, not a
     charge, so it never parks workloads. Object storage itself keeps
     serving within its plan ceilings, as before.
   - apid reserves before it reads compute and egress. So either meterd sees
     the reservation, or apid's read includes every hour meterd billed without
     seeing it.
5. **Adoption.** When an organization adopts its members' buckets mid-month,
   the month's storage moves to the organization.
   - What those members were charged for the month so far (live and shadow
     rows separately) becomes the organization's `credited_millicents`, and
     the organization owes only the rest.
   - A member's charge can include buckets that stay its own, so the month
     can be under-billed. It is never billed twice.
   - Adoption is never refused for this: it activates the organization's
     subscription.
6. **Event.** Each live step sends one `faas_object_storage_usage` event to
   Polar.
   - `charge_millicents` is the step's delta, and the event is timestamped
     when it is sent.
   - The external ID is `faas-object-storage-<account>-<YYYY-MM>-<target millicents>`.
   - Metadata carries the month and the cumulative charge. The month-close
     step also carries the record's detail.
   - The Polar meter (sum of `charge_millicents`) is unchanged.

## Consequences

- A customer who churns is billed for storage up to their last daily step. At
  most one interval of the final month goes unbilled.
- Storage is invoiced in the cycle it accrues in, not a month later.
- As before, usage capped by the overage cap is never billed later.
- A crash between reserve and fix leaves the reservation in place, and meterd
  keeps it away from compute. The next hourly sweep retries the step and
  replaces the reservation. A reservation younger than
  `api.ObjectStorageProgressRetry` (5 min) is left to the apid that is still
  working on it.
- **Rollout.** An N−1 apid that closes a month publishes the whole month
  under the record's external ID, which duplicates the steps an N apid
  already sent. Keep `object_storage_billing_mode` off or shadow until every
  apid runs this release. Production runs it off.

## Alternatives

- **A final partial charge when a subscription ends.** Billing would hang off
  the subscription-ended webhook, and by then the provider may have closed the
  subscription.
- **Hourly steps, like compute.** That is 24 times the events for a meter that
  moves slowly.
- **Judge eligibility at month close from plan history.** The charge still
  lands after the subscription is gone, so the provider does not invoice it.
