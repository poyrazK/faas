# ADR 20261029094512 · Billing state is reconciled with the provider, and only a paid-up subscription ends dunning

- **Status:** accepted
- **Date:** 2026-10-29
- **Amends:** ADR-20261026180850 (billing webhooks follow the current subscription); ADR-20261028101500 (its account of Polar's usage lag)
- **Related:** ADR-1162 (billing delivery finality), spec §4.7 (dunning)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

A billing audit of `main` (2026-10-29) found three gaps.

1. **Nothing repairs a missed webhook.** Account plan and dunning state move
   only on Polar webhook deliveries. Polar retries a delivery and then drops
   it, and it disables an endpoint after 10 consecutive failed deliveries
   until someone re-enables it by hand. apid answered 503 for every known
   event about a customer no account is bound to, so a few such deliveries in
   a row (a customer created outside Gregale, a deleted account) could
   disable the endpoint for every account. After any of these, a missed
   upgrade, cancellation or payment failure stayed wrong for good.
2. **A late payment success ends dunning on an unpaid account.** The
   freshness check (ADR-20261026180850) accepted a payment success for any
   subscription that still grants its plan, including a `past_due` one, and
   `subscription.cycled` (a new period, sent before its renewal order is
   paid) counted as a payment. A delayed `order.paid` for an earlier order, or
   a `cycled` delivery after the renewal failed, moved a past-due account
   back to `active`.
3. **The final cycle's usage at cancellation.** meterd skips Free accounts,
   and the earlier sandbox run (ADR-20261028101500) read Polar as billing a
   meter period one order late. If that held, a cancelled subscription's last
   period of usage would never be billed.

## Decision

1. **apid reconciles each billed account with its provider subscriptions.**
   Every 30 minutes one apid replica (a session advisory lock) lists each
   Polar-billed customer's subscriptions (`GET /v1/subscriptions/?customer_id`,
   unfiltered, because Polar's `active` filter leaves out `past_due`) and
   compares them with the account. A difference becomes the billing event a
   webhook delivery would have carried. The event runs through the same
   transitions and outbox mail selection as a verified delivery:
   - a paid-up subscription on an account in dunning: payment succeeded;
   - a past-due subscription on an active account: payment failed (with the
     email);
   - the account's subscription listed as ended, with no live one: subscription
     ended (downgrade to Free);
   - another live subscription, or a changed product: subscription updated.

   The account's own subscription is preferred over a newer live one. A
   subscription missing from the listing proves nothing, so the account is
   left as it is. A difference is applied only when two consecutive passes
   see the same one, so a delivery in flight settles first. At most 25
   accounts change per pass, and the limits live in `pkg/api/limits.go`
   (`BillingReconcile*`). Each correction is audited
   (`billing.subscription_reconciled`) and counted
   (`apid_billing_reconcile_corrections_total{correction}`). The provider
   surface is the optional `billing.CustomerSubscriptionLister`; providers
   without it are not reconciled.
2. **A delivery for an unbound customer is acknowledged.** Redelivery cannot
   change the answer, so it is logged as `polar_webhook.unknown_customer` and
   answered 200. A failed lookup still answers 503 so Polar retries.
   Reconciliation covers an account whose binding lands later.
3. **Only a paid-up subscription ends dunning.** A payment success delivered
   while the provider still reports the subscription `past_due` keeps its
   invoice, credits and plan, but carries `HoldDunning`, and the account stays
   in dunning. The payment that settles the balance (or reconciliation, once
   Polar reports the subscription active) ends it. `subscription.cycled` maps
   like `subscription.updated`: a period change, never a payment.
4. **The cancellation finding is corrected, not coded around.** A sandbox
   cancellation on 2026-10-29 (`cancel_at_period_end`, period end forced)
   produced a final order 15 minutes after the end. That order billed the
   usage still pending from the previous period. The 5 units ingested 2
   minutes before the end were not billed. Laid next to every earlier sandbox
   order, each usage event Polar billed "one order late" had reached Polar
   within about 3 minutes of a forced renewal. The 17:00 hour, for example,
   was pushed at 18:49:31 and the renewal ran at 18:51:47. So Polar did not
   lag by a whole period. It processes usage events a few minutes after they
   arrive, and an event processed after a renewal is billed on the next
   order. An event processed after the subscription ended is billed nowhere.
   meterd delivers each hour minutes after it is final, so at a cancellation
   the unbilled usage is the hour in progress at the period end and any hour
   delivered within those minutes. That is at most about one hour of
   overage: 40 GB-h ($0.40) on a fully used Scale plan. meterd's skip of Free
   accounts does not change this, because the subscription binding is cleared
   in the same transition that sets Free. A returning customer's undelivered
   hours are still billed on the new subscription within the replay lookback.
   No code changes for this.

## Consequences

- An apid outage longer than Polar's retries, or a disabled endpoint, no
  longer leaves accounts wrong for good. Corrections arrive 30–60 minutes
  after the difference appears. Operators still re-enable a disabled Polar
  endpoint (docs/ops/billing-provider-switch.md).
- One Polar read per billed customer every 30 minutes (200 ms apart, one
  replica). A customer with more than 100 subscriptions fails its read, and is
  never treated as "no subscription".
- A payment success arriving before Polar marks the subscription active leaves
  the account in dunning until the next `subscription.active`/`order.paid`
  delivery or two reconciliation passes, at most about an hour.
- Not verified in the sandbox: whether Polar's final order bills usage
  delivered well before a cancelled period ends (the evidence above supports
  it; disproving it needs an active subscription, so one card checkout). This
  is a go-live check. If the final period turns out unbilled, the fix is a
  settlement cycle: switch a cancelling subscription to a usage-only product
  at period end and revoke it once the last hours are delivered.
- Rejected: replaying webhooks from Polar's delivery log (Polar keeps no
  replay API for disabled endpoints, and replay would still need the
  freshness check); acting on one pass (races an in-flight delivery and can
  flap the plan); a full `SubscriptionState` read per bound subscription (it
  misses a customer who paid but whose activation never arrived).
