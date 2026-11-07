# ADR 20261107103000 · Account flow gaps: recovery, membership, email and alerts

- **Status:** accepted
- **Date:** 2026-11-07
- **Related:** ADR-032, ADR-039, ADR-077, ADR-20261103202000, ADR-20261106210000

## Problem

A review of the platform account flow (signup, sign-in, recovery, profile and
org membership) found gaps that left customers stuck or uninformed:

1. An owner who never verified the address and forgot the password could not
   recover: forgot-password mails only verified accounts, and the verification
   link was re-sent only after a correct password sign-in.
2. Members could not leave an org; self-removal was refused for every role.
3. The account email could not be changed.
4. A fresh verification link needed a password sign-in.
5. The 30-day verification grace was displayed but never enforced.
6. Recovery codes could only be replaced by disabling and re-enrolling MFA.
7. Linked Google/GitHub sign-ins could not be listed or removed, and linking
   one to an existing account sent no notice.
8. Signing in from a new device sent no notice.
9. Sessions, password, email and verification had no CLI surface.
10. Accounts had no display name.

## Decision

**Recovery of unverified accounts.** Forgot-password mails an unverified
account a magic sign-in link instead of a reset link. Redeeming it uses the
existing ownership claim: the password, sessions, keys and authenticator set
before the address was proven are revoked, then the address is marked
verified, and the owner sets a password from the account page. A reset link is
never issued for an unverified account, because a reset would keep the other
credentials of whoever registered the address first.

**Leaving an org.** `POST /v1/orgs/{slug}/leave` ends the caller's own
membership for any role except owner (transfer first). Personal orgs cannot be
left and the billing contact must be replaced first. `DELETE
/members/{user_id}` keeps refusing self-removal so an admin cannot lock
themselves out by accident.

**Email change** (confirm the new address only). Requested from a browser
session, never with an API key, with the same proof as a password change: a
recent step-up when MFA is enrolled, otherwise the current password when one
is set. A single-use link (keyed digest in `account_email_changes`, 1 hour) is
mailed to the new address; the confirm page posts it back, so mail scanners
cannot consume it. Confirming switches the address, marks it verified and
signs out every session (the link may be opened on any device). API keys are
kept. The old address receives an informational notice
without a revert link. A new address that already has an account is answered
like any other request and nothing is mailed; the lookup, token and mail run
in the background so the response time is the same either way. Confirming
also spends every outstanding password-reset link and legacy MFA-disable
request, which are bound to the account rather than the address they were
mailed to. Wrong current passwords count against the dashboard's per-IP
sign-in failure bucket, as on set-password.

**Resend verification.** `POST /v1/auth/verify-email/resend` and an account
page form, bounded by the per-account security-mail throttle.

**Verification grace.** Past 30 days unverified, an account is restricted, not
deleted: password sign-in is refused (a new link is mailed), and existing
sessions and keys reach only the account read, deletion, logout, resend and
email change (the only way out for an address with a typo). Every channel
that proves the address lifts the restriction. Accounts created before
enforcement shipped get their 30 days from `api.EmailVerificationEnforcedFrom`
(2026-11-16), so long-standing unverified accounts and their CI keys are not
cut off on deploy day. In the last 7 days, an account still in use is mailed a
fresh verification link at most once a day per apid.

**Recovery codes.** `POST /v1/account/mfa/recovery-codes` replaces the codes
after a step-up and mails a notice. It takes a browser session only (strict
step-up): recovery codes stand in for the authenticator, so a leaked API key
must not be able to mint them.

**Sign-in methods.** `GET /v1/account/sign-in-methods` lists password, MFA and
linked providers; `DELETE /v1/account/sign-in-methods/{provider}` unlinks one
(browser sessions need a step-up; API keys may unlink) unless it is the last
way to sign in. Linking a provider to an existing account mails a notice.
Unlinking is recorded in `account_oauth_unlinks`: the OAuth email-match path
otherwise re-links any identity with the account's email on the next "Continue
with Google", which would make removal meaningless. A removed provider is
refused there until the owner links it again from a browser already signed in
to the account (the account page's **Link** button); that clears the record.

**Notices.** Security notices (new sign-in, provider linked, recovery codes,
email changed) are mailed off the request path, so a slow mail server does not
delay sign-in.

**New sign-in alert.** A browser session whose user agent and network (/24,
/48) match no session of the last 90 days mails a notice, except on the
account's first sign-in.

**CLI.** `gregale account sessions list|revoke [--all]` (an API key may revoke
browser sessions; CSRF applies to cookie sessions only, and keys are kept),
`account email verify`, `account email update` and `account password update`
(browser handoff, like MFA), `account mfa rotate`, `account update --name`,
`account provider list`, `org leave`.

**Display name.** `accounts.display_name`, at most 80 characters, set with
`PATCH /v1/account`.

Passkeys are out of scope for this change.

## Consequences

The account page and CLI cover the account lifecycle without operator help.
Billing providers keep the customer email they had; billing contact email stays
a separate org setting.
