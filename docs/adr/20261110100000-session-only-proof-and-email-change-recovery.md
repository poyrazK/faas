# ADR 20261110100000 · Session-only proof needs a recent sign-in; email-change warnings and operator revert

- **Status:** accepted
- **Date:** 2026-11-10
- **Related:** ADR-140, ADR-20261107103000, ADR-20261108160000, ADR-20261109120000

## Problem

The 2026-11-08 account-flow audit found that a stolen browser session, a
cookie copied out of a laptop or a shared machine, could take over an
account that signs in with Google, GitHub or email links only:

1. **Session-only proof.** Account changes ask for the strongest proof the
   account has: a fresh authenticator code, else the current password
   (ADR-140). An account with neither had nothing to ask for, so the bare
   session could set a first password, change the sign-in email, remove a
   sign-in method or add an authenticator that locks the owner out.
2. **The owner learned of an email change only after it happened.** The
   previous address was mailed at confirmation, by which time every reset and
   sign-in link already went to the new address.
3. **Removing a provider was silent.** Linking one sent a notice; removing
   one did not.
4. **No way back after a confirmed change.** The notice said "contact
   support", but the recovery runbook said support never changes the email.
   Support had no tool and no procedure.

## Decision

- **Recent sign-in.** When the session is the only proof, it must have
  signed in within `api.RecentSignInWindow` (15 minutes; `sessions.issued_at`
  is the sign-in time and renewing the cookie does not move it). This applies
  to setting a first password and changing the email (the `"session"` rung
  of `accountChangeProof`), removing a sign-in method without an
  authenticator (API and account page), and starting two-factor setup on an
  account without a password (confirm already asks for the password when
  there is one). Refusals answer `403 recent_sign_in_required`; account-page
  forms redirect to `/login?reauth=1&next=/dashboard/account`. The audit kind
  is `auth.recent_sign_in_required`, shown in Security activity. A full-access
  API key still removes providers, as documented.
- **Warn at request time.** Requesting an email change mails the current
  address too ("Someone asked to change your Gregale sign-in email"), with
  the new address masked and how to stop it: sign out everywhere or reset the
  password, both of which cancel pending changes (ADR-20261109120000). It
  has its own mail slot, so the confirm link does not use it up.
- **Unlink notice.** Removing a provider mails the account.
- **Operator revert.** `gregalectl accounts revert-email`
  (`POST /v1/admin/ops/accounts/{id}/revert-email`, strict admin mutation)
  moves an account back from the address its last confirmed change moved it
  to, within `api.EmailChangeRevertWindow` (30 days). One statement
  (`RevertLastAccountEmailChange`) applies only while the account still has
  that address, so it cannot run twice or undo a later change; it marks the
  restored address verified and spends sign-in, reset and MFA-disable links
  issued meanwhile. apid then signs out every browser, forgets remembered
  browsers, revokes interactive sign-in keys, ends pending grants, moves
  billing emails back, audits `account.email_reverted`, and mails the
  restored address with the API keys that still work. Operator accounts and
  a previous address now owned by someone else are refused. Procedure:
  `docs/runbooks/account-recovery-email-changed.md`. Support still never
  moves an account to a new address; the lost-factors runbook says so and
  links the exception.

## Consequences

- An OAuth- or email-link-only customer who left a tab open for more than 15
  minutes signs in again before these four changes. Password and
  authenticator users are unaffected: their proof is unchanged.
- No migration: the revert reads the confirmed rows `account_email_changes`
  already keeps.

## Alternatives considered

- **Email confirmation for every session-only change:** one more round trip
  for the commonest legitimate case, and the attacker may already read the
  mailbox for OAuth accounts whose provider mail they hold.
- **Shorter session lifetime for everyone:** costs every customer, not just
  the four changes that matter.
