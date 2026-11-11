# ADR-20261111140000: cost-control delivery and retained evidence safety

- **Status:** local implementation candidate; native qualification remains required.
- **Amends:** [account compute funding](1746-account-strict-compute.md),
  [plan change dating](20261103121407-final-egress-plan-change-time-held-usage.md),
  and [object-storage budgets](20261020160000-object-storage-in-budgets.md).
- **Context:** the retained cost-control audit found billing retries could change
  an immutable provider event's quantity, plan round trips could disappear,
  and a durable spend-cap hold could suppress a failed notice permanently.

## Decision

1. Freeze each compute/egress delivery quantity in `billing_usage_intents`, keyed
   by provider, subject, meter and UTC hour, before either an external effect or
   a zero receipt. Replicas and retries use the first quantity, including zero.
   A cap or plan edit cannot change an event already selected for delivery.
   Failed intent writes hold delivery; provider idempotency remains mandatory.
2. Atomically enqueue an account/month/cap notice with its hold. A fenced outbox
   survives mail failure and process loss; clearing the hold preserves pending
   notices. The stable provider message identity is account/month/cap. This is
   at-least-once delivery: an ambiguous send can retry the same message identity.
3. An append-only plan-activation history is written by database triggers at
   every account or organization plan mutation, including N-1 writes. meterd
   invalidates its pricing cache on the activation head and replays intermediate
   plans, including returns to the original plan and the previous UTC month.
   This does not reconstruct changes that happened before the migration.
4. Shared strict compute funding uses the smallest **time-weighted** billing
   allowance applicable to any recorded hour segment. It never increases an
   existing month's funded allowance or erases paid commitments. This replaces
   ADR-1746's largest-grant rule, which can exceed billing's allowance after a
   downgrade. The bound is deliberately conservative: all units billed across
   segments lie above this common threshold. Later allowance increases can
   improve the invoice without minting authority in an already funded book;
   a fresh month starts with its own recorded grant.
5. Egress delivery, live cap observations and cross-meter cap reservations use
   the plan held in each usage hour and its overage election, as required by
   ADR-20261103131742 amendment 2 and ADR-20261104204500. Main already
   implements that pricing fix; retain its shared hourly calculation.
   Previously settled hours remain final. The strict egress book keeps its existing conservative pinned
   funding contract; a billable event must still fit that contract.
6. Legacy raw-quantity providers keep their raw commercial contract. Their cap
   clamps the quantity they actually charge, at the shared compute rate; a
   zero cap sends no positive quantity. No allowance is subtracted twice and
   this change does not silently switch their catalog to a net-usage contract.
7. Re-evaluate the prior UTC month during the sampler's bounded finality window.
   Late threshold alerts enter the durable outbox without replacing the current
   month's runtime observation. Historical evaluations never create new preview
   stops or projected-overrun alerts. Policies created after a month closed do
   not receive that month's alerts. A revision edited after close is likewise
   ineligible for the previous month's thresholds.
8. Preserve a scheduler's last-known reached ordinary cap during read failures
   for that UTC month. A successful read or month rollover can release it.
   Cold failures retain the documented availability tradeoff; this ordinary cap
   remains distinct from strict funded execution authority.
9. Supervise physical retirement independently of renewal/helper waits. Bound
   the renewal and nft operation by the original financial deadline, cancel
   in-flight work at expiry and join the helper's owner. Native teardown and
   leakcheck evidence remain a release requirement.
10. Calendar rollover cannot finalize a partial object-storage observation.
    Historical reports preserve stale/partial coverage unless retained evidence
    actually covers the closed period.

## Rollout and compatibility

Apply the additive migration before control-plane binaries. It backfills pending
notices for retained holds and captures plan writes from either release. Keep
strict activation behind its existing platform flag and organization restriction.
Pause provider delivery while rotating meterd replicas, retain sampling and
pending usage, and resume once all delivery writers honor immutable intents.
Mixed old/new provider writers cannot uphold the new quantity invariant.
Use one qualified daemon version across the strict graph; drain strict work
before rollback to a version with the old allowance semantics. Retain books,
intents and notices through reconciliation and do not drop these tables while
an event, notice or funded lease remains unsettled.

The new intent ledger prevents future ambiguous retries from changing quantity.
Already ambiguous provider events from before this migration need reconciliation
against their provider IDs before replay; their accepted quantities cannot be
inferred from the new empty intent table. Activation history likewise cannot
repair unrecorded pre-migration plan transitions.

The runtime-role bootstrap keeps plan history read-only; the schema-owner trigger
is the writer. No native or external provider qualification is inferred from
portable tests. [Remaining work](../financial-controls-remediation.md) retains
organization strict funding, native release evidence and full-invoice coverage.
