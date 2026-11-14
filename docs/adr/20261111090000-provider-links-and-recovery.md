# ADR 20261111090000 · Provider links need a proven address, and recovery removes foreign ones

- **Status:** accepted
- **Date:** 2026-11-11
- **Related:** ADR-20261107103000, ADR-20261108150000, ADR-20261109120000, ADR-20261110100000

## Problem

The 2026-11-10 account-flow audit found that a linked Google or GitHub
identity was a sign-in backdoor that outlived every recovery:

1. **Linking had no guard.** "Link Google / Link GitHub"
   (ADR-20261108150000) linked any provider identity, whatever its email,
   from any signed-in browser: an unverified account, or a session days old.
   Removing a provider already needed fresh presence (ADR-20261110100000);
   adding one did not.
2. **Provider sign-in verified the wrong address.** Signing in through a
   link marked the account's own address verified, whatever address the
   provider had proved. A squatter could sign up as `victim@`, link their own
   Google, and sign in with it: `victim@` became verified. When the real
   owner then claimed the address with an email link, the claim saw a
   verified account and did nothing, and the squatter's Google sign-in
   stayed. The 30-day restriction and the 60-day deletion of unverified
   accounts no longer applied either.
3. **Recovery left links in place.** Password reset, the ownership claim,
   and operator email revert and MFA reset revoked sessions, keys and
   remembered browsers, but never `oauth_links`. Sign-in matches the
   provider subject first, so a thief's linked identity kept signing in.

## Decision

- **Starting a link** from the account page needs a verified email and fresh
  presence, the same as removing one: a code from the last 5 minutes when
  MFA is on, else a sign-in from the last `api.RecentSignInWindow`.
  Refusals are audited as `account.sign_in_method_link_refused` /
  `auth.recent_sign_in_required`.
- **Provider sign-in verifies the account only when the provider's verified
  email equals the account email** (case-insensitive). Otherwise it signs in
  without changing the account's verification.
- **Recovery removes foreign provider links.** The ownership claim (inline
  and durable), password reset, operator email revert and operator MFA reset
  remove every provider that has no link under the account's current email.
  The removal goes through `DeleteOAuthLink`, so it is recorded as unlinked
  and cannot re-link by matching email. Each removal is audited
  (`account.sign_in_method_unlinked` with the recovery as `reason`) and
  mailed. A voluntary password change and a confirmed email change leave
  links alone.

## Consequences

- An owner whose GitHub email differs from their Gregale email loses that
  link on a password reset and links it again from the account page. The
  removal mail says so.
- Existing links made from unverified accounts no longer verify those
  accounts, and are removed when the owner claims the address.
- No migration.
