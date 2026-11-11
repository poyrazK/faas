# ADR 20261109120000 · Account recovery ends pending grants; verification needs the account's browser

- **Status:** accepted
- **Date:** 2026-11-09
- **Related:** ADR-20261107103000, ADR-20261108150000, ADR-20261108160000

## Problem

The account-flow audit of 2026-11-08 found that each recovery step cleared a
different subset of what an attacker who once had the account could have left
behind:

1. **Pending grants survived recovery.** A password reset, a password or
   email change, "sign out everywhere", an ownership claim through an email
   link, and operator suspend/MFA reset revoked sessions and sign-in keys but
   left a pending email change (confirmable from the attacker's mailbox),
   unaccepted invitations the account had sent, and CLI sign-ins approved in
   the browser but not yet redeemed by the CLI.
2. **The durable magic-link claim kept the squatter's authenticator.** The
   inline path cleared TOTP on an ownership claim; the mail-outbox path
   (`ClaimMagicLinkOwnership`) did not, and did not forget remembered
   browsers or end pending grants.
3. **Durable password reset refused past-due accounts.** The inline path
   allowed `past_due`; the outbox path allowed only `active`, so a customer in
   dunning could not reset.
4. **Unverified accounts could mint long-lived credentials.** Keys,
   organizations and invitations were open before verification, so a signup
   squatter could plant a key or org that outlived the owner's claim.
5. **The verify-email link verified the squatter's account.** The link goes
   to the real owner's mailbox; a squatter who set the password could ask the
   owner to "verify", and one click handed the squatter a verified account.
6. **The rotation grace window was unbounded and changed without step-up.**
   A stolen session could set a years-long grace so a rotated key kept
   working.
7. **Hand-made keys survive a reset silently.** A reset keeps keys the owner
   created for CI and deploys, but the owner had no prompt to look for one the
   attacker created.

## Decision

- **One "end pending grants" step** (`state.AccountPendingGrantsStore`,
  `EndAccountPendingGrants`, one transaction) deletes pending email changes,
  revokes unaccepted invitations the account sent, and expires approved but
  unredeemed CLI codes. Every recovery path calls it: password reset and
  change, email change confirm, sign out everywhere (sessions API and account
  page), ownership claim (inline and durable magic link), operator suspend,
  revoke and MFA reset. It is audited as `account.pending_grants_ended` and
  shown in Security activity.
- **The durable claim matches the inline one:** it clears MFA secret,
  recovery codes and enrolment, forgets remembered browsers and ends pending
  grants.
- **Durable reset allows `past_due`,** like the inline path.
- **Verified email is required** to create API keys (personal and org),
  organizations and invitations (`requireVerifiedEmail`).
- **The verify-email link verifies only from a browser signed in to the
  account it was issued for.** Elsewhere it redirects HTML clients to sign-in
  with the link as `next` (`?verify=1` shows why) and answers other clients
  `401 session_expired`; the token is not spent. The mail says so and points
  someone who did not create the account at a sign-in link, which claims it.
- **Grace window ≤ 30 days** (`api.APIKeyGraceWindowMaxDays`); larger values
  get a 400 with the limit, observed value and docs URL, stored larger values
  are clamped, and changing it needs step-up within 5 minutes.
- **A reset lists the keys it left working:** it mails them (kind
  `reset_keys_review`) and lands the browser on the account page with a
  notice, instead of revoking CI keys.

## Consequences

- Clicking the verification link from a mail client that is not signed in
  now takes one sign-in. Owners who never set the password use Forgot
  password or a sign-in link, which already claimed the account.
- Unverified accounts must verify before creating keys, orgs or invitations.
  Existing ones are not revoked.
- No schema change; the new queries reuse existing tables. Release N-1 apid
  keeps working beside N.

## Alternatives considered

- **Revoke every key on reset:** breaks customer CI on every reset; rejected
  in favour of listing them.
- **Make the verify link claim the account (drop the password):** the owner
  who did set the password loses it on a click; the sign-in requirement
  keeps both cases correct.
