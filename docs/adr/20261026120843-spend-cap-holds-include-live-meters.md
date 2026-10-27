# ADR 20261026120843 · The spend cap stops workloads on every live meter, not just compute

- **Status:** accepted (2026-10-26)
- **Date:** 2026-10-26
- **Amends:** [ADR-1162](1162-billing-delivery-finality-spend-cap-and-plan-history.md)
  (a hard monthly spend cap; schedd parks a capped account's workloads)

## Context

ADR-1162 made `accounts.overage_cap_cents` a hard monthly ceiling: the pusher
bills each meter only up to the cap, and schedd refuses wakes, parks live
instances, holds job tasks and skips warm-pool fills while the cap holds.

The billing side counts every live provider meter. With a provider's egress
policy live (`egress_billing_mode = "live"`, off by default), the pusher
reserves the month's billable egress against the same cap
(`pkg/meter/billing_cap.go`), and apid and meterd add it to the account's
overage. schedd's gate (`pkg/sched/overage.go`) compares compute overage alone
with the cap, because only meterd and apid load the provider's meter policies
(Hunt 16 H16-1).

So once egress billing is live, an account whose compute plus egress reaches
its cap keeps running. Compute is billed only up to the cap, and egress past
it is never billed, without limit, while compute overage alone stays under the
cap.

## Decision

meterd records the combined result and schedd enforces it.

- **meterd.** Each quota tick, for every account with a cap, meterd compares
  compute overage plus every live secondary meter's overage with the cap. At
  or past it, meterd upserts a row in `account_spend_cap_holds` with the UTC
  month, the cap it was evaluated against and the observed cents. Below it,
  meterd deletes the row. A failed read leaves the previous row in place.
- **schedd.** When compute overage alone is under the cap, the overage checker
  reads the account's hold. It counts as reached only while the row's month is
  the current UTC month and its cap equals the account's current cap, so
  raising, lowering or clearing the cap takes effect at once and a hold never
  outlives its month. Every existing gate (wake refusal, `appMustPark`, job
  dispatch, warm pool) goes through the checker and needs no change.
- **Failure mode.** As before, a read failure fails open. The pusher's cap
  arithmetic still bounds what is billed; the hold bounds what runs.

The quota tick also stops skipping the Free hard stop and the paid warning
ladder for an account at its cap (Hunt 16 H16-3). The cap limits what is
billed; it never exempts an account from its plan quota.

## Consequences

- A capped account is parked within one meterd quota tick plus one reaper
  tick of reaching its cap on any live meter. Gregale absorbs that window.
- Egress billing can go live without turning the cap into free egress.
- One new expand-only table, written only by meterd and read by schedd; rows
  for accounts that later drop their cap are ignored by schedd and replaced
  or deleted on the account's next evaluation.

## Alternatives

- **Teach schedd the provider policies.** schedd would need the billing
  provider's configuration and egress usage reads on the wake path; meterd
  already computes the total every tick.
- **Bill egress outside the cap.** Contradicts ADR-1162's single hard ceiling.
