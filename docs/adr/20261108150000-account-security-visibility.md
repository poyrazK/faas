# ADR 20261108150000 · Account security visibility: session-bound linking, activity, credential notices

- **Status:** accepted
- **Date:** 2026-11-08
- **Related:** ADR-035, ADR-20261107103000

## Problem

After ADR-20261107103000 three gaps remained in how customers see and control
access to their account:

1. The account page's **Link Google/GitHub** button only worked when the
   provider identity used the account's email. With another email, the OAuth
   callback matched no account and signed the browser in to a new one.
2. Customers could not see their own sign-ins and security changes. The
   events were audited, but the only reader was the general audit log.
3. Only browser sign-ins from an unfamiliar device mailed a notice. A new API
   key, a CLI or agent sign-in, a rotated key, or a password traded for a key
   through the API — each a credential that outlives any session — mailed
   nothing. The CLI approval page is also the target of device-code phishing.

## Decision

**Link by session.** The account page's Link buttons are CSRF-checked forms
(`POST /dashboard/account/providers/{provider}/link`). The handler sets a
five-minute, host-only `faas_oauth_link` cookie naming the account and
redirects to the provider's OAuth start. The callback, after the provider's
state and PKCE checks, sees the intent and links the returned identity to that
account, whatever its email, but only while the browser still carries a live,
fully signed-in session for it. An identity already linked to another account
is refused. Linking clears the account's "unlinked" record for the provider and
mails the usual notice. Callbacks without the intent keep the sign-in behaviour
(subject, then email).

**Security activity.** `GET /v1/account/security-activity` (full-access
credentials only, since it shows client addresses), the account page's
**Security activity** table (newest 20) and `gregale account activity list`
show the last 90 days, at most 100 events. Rows come from `audit_log` for the
account and are limited to an allowlist of sign-in, session, credential,
password, email, two-factor and sign-in-method kinds, each with a fixed
summary; only the recorded address and one short qualifier (method, provider
or label) are taken from the payload. Legacy `key.*` duplicates are skipped.

**Credential notices.** Creating or rotating an API key (account or org),
exchanging a CLI or agent (MCP) sign-in code, and trading a password for a
key through the API mail the owner, off the request path and under the
per-account security-mail throttle. The key a signup mints for itself (account
under 10 minutes old) is not reported.

**Remember this browser.** Customers with two-factor authentication entered a
code at every dashboard sign-in. The sign-in authenticator check now offers
"Remember this browser for 30 days". Ticking it stores a row in
`mfa_trusted_browsers` (keyed digest of a random 32-byte token, the browser's
user-agent family, address, expiry) and sets a host-only, HttpOnly cookie with
the token. A later sign-in that carries the cookie, from the same browser
family, starts a full session instead of an MFA-pending one, and records
`auth.mfa_trusted_browser_used`. The trust:

- skips only the sign-in check; step-up for sensitive actions still asks for
  a code;
- never applies to operators, or to an account without an enrolled factor;
- ends after 30 days (`api.MFATrustedBrowserTTL`); an account keeps at most 10
  (`api.MFATrustedBrowsersMax`), oldest forgotten first;
- does not survive turning two-factor off and on again (a browser remembered
  before the current enrollment is ignored);
- is forgotten for every browser on a password or email change, signing out
  everywhere, and an operator suspend or session revoke.

The account page lists remembered browsers with **Forget** and **Forget all
browsers**.

## Consequences

Customers can link any provider identity they control and audit their own
account without support. More mail goes to accounts that create keys often;
the per-kind throttle (one a minute, five an hour) bounds it, so a burst of
key creation reports the first ones only.

A remembered browser trades one factor for the cookie on that device: someone
who steals the cookie and the password, magic link or provider sign-in, and
also matches the browser family, signs in without a code. The 30-day limit,
the per-account list, forgetting on credential changes and the unchanged
step-up gate bound that.
