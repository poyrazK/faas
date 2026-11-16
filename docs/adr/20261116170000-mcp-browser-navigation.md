# ADR-20261116170000: MCP browser handoffs finish the form before navigating

Date: 2026-11-16
Status: Accepted
Amends: ADR-20261028205920 (agent sign-in), ADR-20261026195952 (hosted management MCP)

## Context

An actual Antigravity connection reached Gregale's authorization page, but
Continue to Gregale appeared to do nothing. Chromium blocked the form's 303
redirect from the API origin to the console under `form-action 'self'`.
The same problem affects the final redirect to a client's external callback.
HTTP-only fixtures accepted those responses without exercising the browser's
navigation policy. The unstyled consent page also exposed apid's loopback
listener as the Gregale API, and the sign-in retry fallback opened another tab.

## Decision

For requests explicitly accepting HTML, the bridge completes either handoff
with an HTML document. Its meta refresh starts a new navigation to the already
validated native sign-in URL or registered OAuth callback. A normal link is the
fallback. Neither a new redirect endpoint nor browser script is required.
Requests that do not accept HTML retain the existing 303/Location behavior.
Both representations vary on Accept and remain uncached with no referrer.

The bridge keeps `default-src 'none'`, `form-action 'self'`, `base-uri 'none'`
and `frame-ancestors 'none'`. Its exact static stylesheet is allowed by a CSP
SHA-256 hash. There are no external assets or inline script permissions.
The consent page names the client, explains the steps, groups optional tool
selection separately from access approval, and shows the public MCP resource
in its connection details on hosted installations. Self-hosted connections keep
their configured API identity. The hosted retry link remains in the same tab.

The failed-authentication error hint includes the same delay as Retry-After;
the rate-limit budgets, keys and enforcement remain unchanged.

## Validation

The browser-shaped fixture uses Antigravity's published client metadata and the
packaged redirect allowlist. It covers native sign-in, fresh CSRF on return,
approval and denial, exact callback state/issuer, PKCE token exchange and MCP
tool discovery. It checks document navigation plus its fallback, the stylesheet
hash and the unchanged private-document policy. Existing non-HTML OAuth tests
continue to require 303 responses. Hostile title/destination values stay escaped.

A local Chromium fixture using the consent renderer and navigation helper also
completed form-to-sign-in navigation from `127.0.0.1` to `localhost` and the
reverse callback under the strict policy. It used synthetic pages and no account
credentials; native API behavior is covered separately by the integration fixture.

Live acceptance still requires releasing the bridge and retrying Antigravity
after any active sign-in rate-limit window. A local fixture is not evidence of
a production release or a completed user connection.
