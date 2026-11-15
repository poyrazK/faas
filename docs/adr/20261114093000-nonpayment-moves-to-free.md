# ADR-20261114093000: Non-payment moves the account to Free; nothing is deleted

- **Status:** accepted
- **Date:** 2026-11-14
- **Milestone:** M7 billing launch readiness (billing review 2026-11-13, R9-1)
- **Amends:** spec §4.7 dunning; [ADR-20261017125602](20261017125602-mail-nonpayment-deletion-notices.md)

## Context

Spec §4.7 ran a failed payment through `past_due` (7 days) → `suspended`
(apps parked) → 21 days → `deleted_pending`. The Terms (§3) say a failed
payment may limit paid features or move the account to the Free plan, and that
no data is deleted for a failed payment without notice and a chance to pay or
export. The ladder contradicted them: it parked every app and scheduled
deletion, and once Polar revoked the subscription there was no path to settle
and carry on as a Free customer. The owner chose "move to Free, no deletion".

## Decision

1. A failed payment still moves the account to `past_due`: apps run, deploys
   are blocked, the customer is mailed.
2. Seven days later meterd's dunning sweep moves the account to the Free plan
   and back to `active`, keeps `past_due_at` as the unpaid-balance marker, and
   sends one "moved to Free" mail (`billing_past_due` notify with
   `"kind":"moved_to_free"`). Resources above the Free plan follow the
   ordinary plan-excess rules; the Free quota stop applies as for any Free
   account.
3. Nothing is suspended or scheduled for deletion for non-payment. Meterd no
   longer issues the durable suspension or non-payment deletion commands.
   Accounts the retired ladder already suspended move to Free on the next
   sweep; accounts already in `deleted_pending` keep their existing follow-up.
4. An active (or Free-quota-stopped) account with `past_due_at` set is "moved
   to Free for non-payment" (`Account.MovedToFreeForNonpayment`). Payment
   failures, past-due pings and updates for the old subscription leave it
   where it is: they neither restart dunning nor re-bind the paid plan. The
   30-minute billing reconciliation follows the same rule.
5. Only a paid order (payment success on the subscription, or a new paid
   subscription) or an operator clears the marker. The Free quota stop and its
   lifting keep it.

## Consequences

The Terms and the product agree, and no customer data is lost for a missed
payment. The debt is tracked rather than enforced: a moved-to-Free customer
can keep using the Free plan without paying. The internal-mail suspension and
deletion outbox paths stay in apid and maild, unused by meterd; removing them
is separate cleanup. `meterd_dunning_deletion_blocked_accounts` stays
published at zero for its existing alert.
