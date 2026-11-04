# ADR-20261026112500: The session cookie is `__Host-faas_sid`

Date: 2026-10-26
Status: Accepted
Amends: [ADR-039](039-server-side-session-revocation.md) (cookie name)
Related: [ADR-20261026100000](20261026100000-platform-and-app-domains.md) (step 4 of moving apps off the platform domain), [ADR-076](076-session-binding-hash.md)

## Context

The dashboard and API session cookie `faas_sid` is host-only, HttpOnly,
Secure and SameSite=Lax. A host-only cookie cannot be read by other hosts, but
it can be shadowed. Any page on another host of the registrable domain can
set `faas_sid` with `Domain=gregale.dev`. Tenant apps run on
`<slug>.gregale.dev`, and gregale.dev is not on the Public Suffix List. The
browser then sends both cookies to `api.gregale.dev`, and which one the server
reads depends on cookie order. A tenant page could therefore plant its own
session in a visitor's browser, which is session fixation and login CSRF.

The `__Host-` prefix closes this. A browser accepts a `__Host-` cookie only
when it is Secure, has `Path=/` and has no `Domain` attribute, so only the
platform host itself can set one. Moving apps to their own domain
(ADR-20261026100000) closes it too, but the prefix also holds for every host
that stays on the platform domain.

## Decision

The session cookie is `__Host-faas_sid`. `pkg/sessioncookie` owns the names,
and every reader, writer and CLI goes through it.

- **Read**: `__Host-faas_sid` first, and the unprefixed `faas_sid` only when
  no prefixed cookie is present. A planted `faas_sid` never replaces a
  session the browser holds under the prefixed name.
- **Write**: an HTTPS response sets `__Host-faas_sid` (`Path=/`, no
  `Domain`) and expires `faas_sid`. A plain-HTTP install, where browsers
  refuse `__Host-` cookies, keeps writing `faas_sid`.
- **Clear** (logout, revoked or invalid session): both names are expired.
- **No in-place upgrade.** A request carrying only a legacy cookie is not
  re-issued under the new name: the legacy cookie may be one a tenant page
  planted, and promoting it would make the fixation stick. Browsers move to
  the new name at their next sign-in.
- **Clients**: the CLIs and `pkg/api` send a stored session under both names
  and read either from a login response, so they work against servers on
  either side of the change.

## Removing the legacy name

Sessions last 7 days. A release made at least 7 days after this one reaches
production stops reading `faas_sid` in browsers, which closes the fixation
window that the fallback leaves open for signed-out visitors. It also
regenerates the OpenAPI cookie parameter and the SDKs, and stops the CLIs
sending the legacy name. Until then, plain-HTTP installs keep using the legacy
name.

## Consequences

- Session-bearing requests stay classified as cookie-authenticated by the
  standby write gate (ADR-089) under either name, and the dev bridge strips
  both from relayed requests.
- The web console needs no change: its rewrites pass `Set-Cookie` through,
  and a `__Host-` cookie without `Domain` is valid on the console host.
- The OpenAPI document and SDKs still name `faas_sid` until the removal
  release; they keep working through the fallback.

## Rejected alternatives

- **Rename with no fallback.** Every signed-in customer would be logged out
  at deploy, and older CLIs would break immediately.
- **Upgrade legacy cookies in place.** This would promote a planted cookie,
  as above.
- **Rely on the apps-domain move alone.** That protects nothing for hosts
  left on the platform domain, and it depends on a domain purchase.
