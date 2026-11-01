# ADR 20261101143527 · Billing settles hours on Free, delivers a deleted organization's usage, and reserves managed PostgreSQL steps

- **Status:** accepted
- **Date:** 2026-11-01
- **Amends:** ADR-1162 (usage delivery), ADR-20261031110037 (managed PostgreSQL overage invoicing)
- **Related:** ADR-20261030093512 (time-weighted allowance), ADR-20261027093220 (object storage charge steps), ADR-20261029094512 (subscription reconciliation)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

A fourth billing audit of `main` (2026-11-01) found:

1. **Usage on Free could be billed after a new subscription.** The pusher
   skipped a subject on Free without settling its hours, and its pending
   query lists only subjects that are paid now. `billing_from` is fixed at
   the first checkout. When a customer cancelled, kept running on Free and
   subscribed again within the pusher's 30-day lookback, the Free hours came
   up as pending and were billed against that month's allowance: overage for
   usage the customer ran on the Free plan.
2. **An organization's last usage was dropped when it was deleted.** The
   pusher delivers a deleting subject's hours that began before its deletion
   request (ADR-1162's fix for "run heavy usage, then delete"). Organizations
   had no deletion time, so an organization pending deletion delivered none
   of its undelivered hours.
3. **A managed PostgreSQL overage step could be charged twice.** apid sent a
   step to the provider and recorded it afterwards, with the provider's
   idempotency key on the step's target. When recording failed, apid crashed
   in between, or two replicas computed steps from the same base, the next
   step started from the same base with a new target: the provider charged
   both. Object storage avoids this by reserving its step first.
4. **One refund could disable the webhook endpoint.** A refund for an order
   apid had never projected failed the invoice lookup, so the webhook
   answered 503 on every retry. Polar disables an endpoint after repeated
   failures, which would stop every billing webhook.

## Decision

1. **The pusher settles hours on Free.** An hour whose plan, by the month's
   recorded activations (`state.MonthPlanAt`, the rule the allowance uses),
   is Free is settled with a zero receipt, for compute and for secondary
   meters. It never bills, however late it comes up. A store without
   recorded activations keeps the current plan.
2. **Organizations record when deletion was requested.**
   `orgs.deletion_requested_at` (migration `20261101143525306`, expand) is
   stamped by a trigger whenever `deleted_pending` turns on, on every path
   and for the previous release's writes; organizations already pending
   deletion take their last update time. The organization billing subject
   carries it, so the pusher delivers an organization's hours that began
   before the request, as it does for accounts.
3. **Managed PostgreSQL overage steps are reserved before they are sent.**
   `managed_postgres_overage_pending` (same migration) holds at most one step
   per provider, subject and month. A pass finishes a reserved step before
   computing a new one, sending it unchanged; the provider deduplicates it on
   its target. Recording the delivery and clearing the reservation happen in
   one transaction. A replica that finds a reservation leaves it to its
   holder. A step reserved in live mode is not completed while delivery is in
   shadow mode.
4. **A refund for an unknown order is acknowledged.** apid records it in the
   audit log (`invoice_known: false`), logs it and answers 200; no refund row
   is written, because there is no invoice to attach it to.

## Consequences

- A customer who returns from Free is billed only for usage on a paid plan.
- Deleting an organization no longer escapes billing for its last hours.
- Managed PostgreSQL overage can be turned on without double-charge risk
  from failed records, crashes or a second apid.
- A malformed or foreign refund cannot take the billing webhook down.
- The financial ledger still counts Free-plan usage at a zero price, so its
  dual run may report drift for a month in which a subject returned from
  Free to a paid plan.
