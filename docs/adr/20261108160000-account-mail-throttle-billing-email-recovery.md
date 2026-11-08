# ADR 20261108160000 · Shared account mail limits, billing email follows email changes, operator MFA reset

- **Status:** accepted
- **Date:** 2026-11-08
- **Related:** ADR-20261107103000, ADR-20261108150000, ADR-20261103131742

## Problem

Three gaps were left after the account-flow work:

1. **Mail limits were per process.** Security mail (password reset, magic link,
   verification, notices) is capped per account and kind: one a minute, five
   an hour. The verification-deadline reminder is capped at one a day. Each
   apid kept those counts in memory. Gregale runs several apid processes, so
   an account could get each cap once per process, and a restart reset them.
2. **The billing email stayed behind.** A confirmed email change moved the
   sign-in address only. The billing provider's customer, and an
   organization's billing email set from that member at its first checkout,
   kept the old address. Receipts, dunning mail and the billing portal's
   one-time sign-in went to a mailbox the customer may have just lost.
3. **No way back for a customer without any factor.** Someone who lost the
   authenticator, the recovery codes and the mailbox could not recover: the
   email disable path mails the lost mailbox. Support had no tool and no
   procedure short of editing the database.

## Decision

**Shared mail counter.** Table `account_mail_throttle` holds one row per
account and mail kind: the start of a fixed window, the sends in it and the
last send. One conditional upsert (`TakeAccountMailSlot`) counts a send only
when the window has room and the minimum interval has passed, so concurrent
apids cannot both take the last slot. Security mail keeps its limits (fixed
one-hour window, five sends, one minute apart). The verification reminder uses
kind `verification_reminder`, one per 24 hours. When the store has no counter
or the count fails, apid falls back to its in-process counter instead of
sending without a limit or refusing. A fixed window allows up to twice the cap
across a window boundary. That is acceptable for these limits and avoids
storing every send.

**Billing email follows the member.** After a confirmed email change, off the
request path:

- the account's own billing customer moves to the new address;
- every non-personal organization the account belongs to whose billing email
  is the old address moves to the new one. It uses the same
  `orgbilling.ChangeEmail` path as `PUT /v1/orgs/{slug}/billing/email`, so the
  provider customer moves first and the subject lock orders it against
  concurrent changes. The organization had chosen this member as its contact,
  so the contact follows the member.

A provider refusal leaves that billing email unchanged. Refusals include
another customer holding the address (`billing_email_in_use`) or the provider
being down. The account is audited (`account.billing_email_sync_failed`) and
the new address gets a notice naming the subjects to fix.

**Operator MFA reset.** `POST /v1/admin/ops/accounts/{id}/reset-mfa`
(`gregalectl accounts reset-mfa`) is an operator mutation with the same gates
as suspend: admin allowlist, step-up, `confirm=true` and a reason slug.

- **What it does:** clears the authenticator and recovery codes, revokes every
  session, forgets remembered browsers, audits `account.mfa_disabled` (method
  `operator`), and mails the account's address.
- **What it refuses:** operator accounts and accounts without a factor.

The identity checks and the 72-hour notice period are procedure, in
`docs/runbooks/account-recovery-lost-factors.md`:

- two proofs from different kinds;
- a notice to the account and the owners of its shared orgs;
- a second operator's review.

Support never changes the sign-in email; the owner does after signing in.

## Consequences

- One account gets its mail caps once across the fleet, and restarts do not
  reset them. The counter adds one upsert per capped mail.
- Organizations keep billing to the person they chose after that person
  changes address. An organization that wants a shared billing address sets
  it explicitly; such an address never matches a member's old email.
- Support can recover a customer who lost every factor, at the price of a
  72-hour delay and two proofs. The delay is enforced by people, not code: a
  self-serve recovery with an enforced waiting period remains possible later.
