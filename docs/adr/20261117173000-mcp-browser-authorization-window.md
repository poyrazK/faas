# ADR-20261117173000: One bounded hour for MCP browser authorization

Date: 2026-11-17
Status: Accepted
Amends: ADR-20261028205920 (agent sign-in), ADR-20261116170000 (browser handoffs)

## Context

Production Antigravity sign-in exposed two independent five-minute deadlines:
the bridge's browser authorization/cookie and apid's native MCP approval code.
MFA could outlast the native code. Reviewing apps after sign-in could outlast
the browser authorization and its unapproved grant. Either failure required
starting again from the client, despite a successful sign-in.
The native MFA and approval forms also had a ten-minute CSRF lifetime.

## Decision

`api.MCPBrowserAuthorizationTTL` is one hour. It bounds the whole browser setup
from its start through sign-in, MFA and final access review. The bridge uses
it for the authorization request and its secure browser cookie. Pending grants
remain capped by that request's original deadline and the credential expiry.
Activity does not slide the setup deadline, so repeated visits cannot keep an
unapproved connection alive indefinitely.

The native MCP approval's sealed CSRF token and cookie use the same window.
An MFA form uses it only when its validated same-origin `next` points to a
live, pending MCP approval code. Other MFA forms and ordinary dashboard/CLI
CSRF tokens retain their ten-minute lifetime. Named cookies, account/action
binding and expiry verification remain mandatory; callers cannot request an
unbounded CSRF lifetime.

Only purpose-bound native MCP approval codes use the longer window. Their
256-bit entropy and purpose hash binding remain unchanged. Ordinary terminal
pairing codes, including the device-code and legacy CLI paths, still expire
after five minutes. The browser pages state the setup window.

The final OAuth authorization code still expires after one minute, is bound to
the registered redirect, client, resource and S256 PKCE verifier, and exchanges
once. Access/refresh token lifetimes and the approved connection's credential
lifetime do not change. Native approval still requires authenticated CSRF and
recent sign-in/MFA. Consent still requires the bound browser cookie, current
CSRF token and explicit access choices.

The bridge's authorization rate limits, capacity and eviction policy remain
in force. Persisted transactions retain their recorded expiry; a new release
does not extend setup attempts that already expired.

## Validation

HTTP regressions simulate a 20-minute MFA step and a further 20-minute access
review, finish the callback and PKCE exchange, and call an authenticated MCP
read tool. They verify the one-hour secure cookie, CSRF rotation/rejection,
single-use final code and one-minute final-code expiry. Separate cases expire
the transaction after one hour before sign-in, during MFA and during consent.
Native API tests cover read, write and scoped MCP codes alongside both CLI
pairing formats, which retain their five-minute lifetime.
The native MFA route accepts a current TOTP after a simulated 20-minute form
delay, preserves the original approval destination and issues the matching
longer approval CSRF token. Default forms and unissued MCP links keep the
short CSRF window; separate middleware tests pin binding and final expiry.

## Consequences

People have a bounded hour to finish connection setup without restarting for
ordinary MFA or access-review delays. Unapproved records occupy capacity for
longer, still under the existing fixed limits. No automatic consent or new
credential renewal endpoint is introduced.
