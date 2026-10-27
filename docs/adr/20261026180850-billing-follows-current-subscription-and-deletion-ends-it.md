# ADR 20261026180850 · Billing webhooks follow the current subscription, and deletion ends it

- **Status:** accepted (2026-10-26)
- **Date:** 2026-10-26
- **Context for:** spec §4.7 (dunning ladder, subscription end) and §17 G6
  (30-day deletion grace)

## Context

Hunt 17 found two ways the billing lifecycle charges or punishes the wrong
customer.

**H17-1.** Provider webhooks are neither ordered nor prompt. Polar does not
guarantee delivery order, and apid answers 503 on any transient failure
(database, deploy, a held mail recovery), so the provider retries later. Both
payment-failure paths move an account from `active` to `past_due` whenever the
delivery is first processed. A `subscription.past_due` retried after the
customer paid restarts dunning on a paying account: deploys blocked, apps
parked after 7 days, non-payment deletion after 21. Nothing corrects it until
the next cycle's `order.paid`. Subscription and plan updates have the same
flaw: whichever delivery arrives last wins.

**H17-2.** Neither account deletion (`DELETE /v1/account`, the grace hard
delete) nor organization deletion touched the billing provider. Polar kept
charging the plan fee every month after the customer and its data were gone.
After the hard delete the billing identity cascades, so those charges' webhooks
found no account and were retried forever.

## Decision

1. **A subscription webhook is checked against the provider's current
   subscription before it moves the account.** A provider that can report
   state (`billing.SubscriptionStateProvider`; Polar's
   `GET /v1/subscriptions/{id}`) is read for every payment-failure,
   subscription-update and payment-success delivery that names a
   subscription:
   - a payment failure for a subscription that is now `active` or `trialing`
     is stale: it is acknowledged with an audit row
     (`billing.webhook_stale_ignored`) and changes nothing;
   - an update for a subscription that is no longer live (not active,
     trialing or past_due) is stale in the same way;
   - the plan a delivery grants is the subscription's current product, not the
     product the delivery carried;
   - a payment success for an ended subscription still counts as a payment
     (invoice, credits, dunning recovery), but never re-binds the subscription
     or grants its plan.

   A failed read applies the delivery as sent, today's behaviour, so a
   provider API outage cannot stop dunning or payments. Stripe and Paddle,
   compatibility paths, do not report state and are unchanged.

2. **Deleting an account or organization cancels its subscription at period
   end.** The already paid period runs out and nothing is charged again. A
   subscription the provider reports as ended or unknown needs nothing. The
   cancellation runs before the deletion is recorded; if the provider refuses
   it, the deletion is refused with 503 and the customer retries.
   - Restoring an account within the grace window does not resume the
     subscription. It ends at period end, the account moves to Free, and the
     customer re-subscribes through checkout.
   - The grace hard delete cancels any subscription still live before it
     erases the account, and keeps the account (the existing deferred-cleanup
     gauge) until the cancellation succeeds. This covers non-payment
     deletions, which do not cancel at scheduling time so that a late
     successful charge can still restore the account, and deletions scheduled
     before this change.
   - Organizations are never hard-deleted, so their cancellation runs only at
     deletion, after the managed-database check.

## Consequences

- Every Polar subscription webhook costs one provider read, bounded at 5 s.
- A provider outage delays a deletion (request refused, hard delete
  deferred) instead of leaving a deleted customer paying.
- Lost success deliveries are still not detected: the dunning sweeper does not
  ask the provider before suspending. That remains a known gap.
