# Sales roles, per-salesperson agreements and organization attribution

- **Status:** accepted
- **Date:** 2026-11-20
- **Extends:** [20261111113147 independent sales frontend](20261111113147-independent-sales-frontend.md); see [operations](../ops/sales-referrals.md)

## Context

The sales workspace shipped with two roles (manager, rep) taken from email
lists in the apid environment, one program-wide commission rate, and sales
codes that apply only to a personal account. Sales agreements differ per
salesperson, so the business needs to separate each salesperson's sales, keep
each one's own terms, correct wrong credit, record what was paid, and sell to
companies. Changing staff must not need a deploy.

## Decision

**Three roles.**

| Capability | Salesperson | Sales manager | Owner admin |
| --- | --- | --- | --- |
| Own code, own customers, own leads and tasks | yes | yes | yes |
| Every salesperson's customers, leads, tasks, reports, inquiry routing | no | yes | yes |
| Add, deactivate or reactivate salespeople | no | yes | yes |
| Add, change or remove managers; promote a salesperson | no | no | yes |
| Set a salesperson's commission agreement | no | no | yes |
| Move a customer's sales credit to another salesperson | no | no | yes |
| Record or void a commission payout | no | no | yes |

A salesperson sees commission rate, estimate and payout status only for their
own customers. Owner admins come only from `FAAS_SALES_ADMIN_EMAILS`, so no
UI action can remove the last admin. Managers and salespeople are stored in
`sales_team_members`. Owner admins manage everyone
(2026-11-23 amendment: sales managers manage salespeople only. The team list omits
agreements for managers, who still see the rate frozen on each customer they
can read. Nobody can change their own role or deactivate themselves). The existing
`FAAS_SALES_MANAGER_EMAILS` and `FAAS_SALES_EMAILS` lists still grant those
roles, shown as config-managed and read-only in the UI. The strongest grant
wins. Every role still needs an active account, verified email and an enrolled
authenticator. Removing a member deactivates the row and keeps history.

**Per-salesperson agreements.** `sales_commission_agreements` is an
append-only history of `(salesperson, commission_bps, effective_from, note)`.
When a customer is attributed to a sales code, the owner's agreement in force
at that moment is copied onto the attribution. Without one, the program default
`FAAS_SALES_COMMISSION_BPS` is used. A new agreement affects new customers
only. Commission stays one-time, on the first paid invoice (pre-tax), with the
existing conversion window, hold and reversal rules.

**Credit reassignment.** An owner admin can move a sales attribution to another
active salesperson, with a required reason. The move takes the new owner's
current agreement and writes a `referral_credit_changes` row with old and new
owner and rate. A move is refused once a payout is recorded for that customer,
so paid history stays with the person who was paid. Customer-program referrals
are never converted into sales credit. Lead (CRM) reassignment still does not
change credit.

**Payout ledger.** `sales_commission_payouts` records that an owner admin paid
a salesperson for one customer: amount, currency, date and an external
reference. A customer can have at most one live payout. A payout can be voided
with a reason and recorded again. Recording requires the estimate to be
`eligible`. This is bookkeeping only: Gregale still moves no money and has no
payout integration.

**Organization purchases.** A sales code can be entered before a company
organization's first paid purchase by a billing-capable member of that
organization. Credit belongs to the organization (`subject_kind = 'org'`),
and `selected_by` records the member who entered the code. The attribution
key moves from `accounts(id)` to `resource_namespaces(id)`, which personal
accounts and organizations already share and which invoices reference. Delete
triggers on `accounts` and `orgs` keep the old erase-on-delete behavior.
Customer-to-customer codes remain signup-only and personal-only.

## Consequences

- Staff changes take effect on the next request without a deploy, and every
  change is audited.
- Reports and lists show the role, the per-customer rate and payout state.
- Rollback: with `FAAS_SALES_ENABLED=0` everything stays hidden. The new
  tables are additive. Older apid ignores DB members, agreements and org rows.
