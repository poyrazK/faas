# ADR-1320: Object-storage plan allowances, plan ceilings, and the overage cap

Status: accepted (mechanism only; every plan's object-storage values are zero
until pricing is decided, so shipping it changes no bill and no limit).

Amends ADR-156 (object-storage accounting) and ADR-1162 (the hard spend cap).

Amended by [ADR-20261027093220](20261027093220-object-storage-billed-daily.md):
object storage is billed in daily steps inside its month and takes cap
headroom as it is billed; meterd reserves it before compute and egress.

## Context

Object storage is billed once per UTC month at month close
(`billing.FinalizeObjectStoragePeriods`), from provider usage reports, as a
single line item per account. A billing review found three gaps:

1. **No plan allowance.** Month close priced every stored byte-hour, request
   and egress byte. A plan could not include any object storage.
2. **The plan did not bound object storage.** Only the operator safety policy
   (`ObjectStoragePolicy`) limited an account, and every plan got the same
   bytes, requests and egress.
3. **The spend cap did not cover object storage.** The overage cap clamps
   compute and live egress hour by hour (ADR-1162), but the month-close
   object-storage line item was published in full, so a capped account could
   be billed past its cap.

## Decision

1. **Plan limits live in the limits table.** `Limits.ObjectStorage`
   (`api.ObjectStoragePlanLimits`) holds ceilings (`MaxAccountBytes`,
   `MaxMonthlyRequests`, `MaxMonthlyEgressBytes`) and allowances
   (`IncludedBytes` as average stored bytes over the 730-hour billing month,
   `IncludedRequests`, `IncludedEgressBytes`). A zero field changes nothing.
   Every plan ships at zero; the values are a pricing decision, and a test
   pins them at zero until that decision updates it.

2. **Allowances are removed before pricing.** `objectstorage.CalculateCharge`
   prices only usage above the allowance, per meter. The month's allowance
   follows its plan history like compute (ADR-1162): the per-meter maximum
   among the plans whose activations were recorded in the month, else the
   current plan, so a downgrade never shrinks it. The finalized record copies
   the allowance it used (`included_*` columns) next to the rates. The live
   usage estimate applies the current plan's allowance.

3. **Ceilings tighten the operator policy.** URL admission and the gateway
   request and egress checks enforce the operator policy tightened by the
   plan's ceilings: per ceiling, the tighter positive value wins. The usage
   snapshot carries the plan's limits, read with it (Mem and Pg stores). The bucket cap follows a tighter account ceiling, so the
   tightened policy stays valid; otherwise every check would read the usage as
   stale and refuse. The usage endpoint reports this effective policy.

4. **The overage cap covers object storage.** At month close, the shadow and
   live delivery charges `min(record total, cap − billable compute − billable
   live egress)` for the month:
   - Compute is `billing.OverageMBSecondsForRange`, which is what meterd
     delivered hour by hour before its own cap clamp.
   - Live egress is priced by the provider's egress policy from its
     effective-from, as meterd prices it.
   - The arithmetic is exact (`billing.CapRemainingMillicents`), now shared
     with meterd.

   The record keeps the full priced usage. The delivery receipt and the
   provider event carry the capped charge, and the event carries the uncapped
   total as `uncapped_millicents`. Capped usage is never billed later. If the
   cap cannot be read, the account fails closed: nothing is published, no
   receipt is written, and the next run retries.

## Consequences

- Object storage is last under the cap. Compute and egress settle hourly
  during the month, while object storage settles only at month close. As in
  ADR-1162, reservations use billable rather than delivered cost, so the cap
  can be under-used but is never exceeded.
- Month close reads the cap in force when it runs, early in the next month.
  If the customer raises the cap before then, more of the closed month's
  object storage is billed. Once a receipt exists, the charge is final.
- If a publish is ambiguous and the cap changes before the retry, the retry
  computes a different charge. Polar dedupes on the record's external ID, so
  the first event stands, but the receipt records the recomputed charge.
- At the cap, object storage keeps serving within the plan ceilings, and its
  usage above the cap is not billed. Unlike compute, it is not parked; the
  plan ceilings are its hard stop.
- Month close uses the current plan's egress allowance to reserve egress. If
  the plan changes after the month ends, the reservation can differ slightly
  from what meterd delivered.

## Rejected alternatives

- *Clamp the record's total.* This breaks the `total = storage + requests +
  egress` check and loses the priced usage that audits and disputes need.
- *Count object storage in `CurrentMonthOverageCents` and park it at the cap.*
  This needs object-storage pricing in schedd and a cap read on every gateway
  request.
- *Reserve compute using the month-end allowance.* After a mid-month upgrade,
  this understates compute that was already billed, so object storage could
  spend cap that compute had already used.
- *Let plan ceilings replace the operator policy.* The operator policy is the
  deployment's safety bound; a plan may only tighten it.

## Not covered

Plan values for object storage, budget notifications, and a runtime cap gate
for object storage.
