# ADR-830: Platform security headers on customer app responses

- **Status:** accepted (2026-10-09)
- **Date:** 2026-10-09
- **Amends:** spec §11 "Response headers (issue #249)"
- **Decision:** accepted by the product owner on 2026-10-09; implemented
  alongside this ADR.

## Context

Issue #249 added five static hardening headers through `pkg/httpsec.Static`:

| Header | Value |
|---|---|
| `Strict-Transport-Security` | `max-age=31536000; includeSubDomains` |
| `X-Frame-Options` | `DENY` |
| `X-Content-Type-Options` | `nosniff` |
| `Referrer-Policy` | `strict-origin-when-cross-origin` |
| `Permissions-Policy` | `camera=(), microphone=(), geolocation=(), usb=(), payment=()` |

The spec justifies the frame and permissions values with "the dashboard is
the only HTML surface". In practice the middleware wraps every response from
the public edge, customer apps included, and the values are forced:

1. `gatewayd-internal` mounts `httpsec.Static` on its public handler, so the
   platform values are set before the app response is copied.
2. `gatewayd-public` drops every copy of these five headers from the
   upstream response (`pkg/gateway/internal_proxy.go`, `IsStaticHeader`) and
   its own `httpsec.Static` writes the platform values.

So an app cannot set or relax any of them, and neither can a `kind=headers`
edge rule (its response operations run in `gatewayd-internal` and are dropped
at the same point). Concretely:

- **HSTS `includeSubDomains` on custom domains.** A customer who serves an
  app at `example.com` pins every subdomain of `example.com` to HTTPS for a
  year in every browser that visits it, including hosts Gregale does not
  serve (an internal `http://` tool, a vendor CNAME without TLS). Once a
  browser has cached it, removing the header cannot unpin it before
  `max-age` expires. The customer never chose this.
- **`X-Frame-Options: DENY`** blocks embedding a customer app in an iframe:
  widgets, embedded checkouts, dashboards inside a customer's own portal.
- **`Permissions-Policy`** disables camera, microphone, geolocation, USB,
  and the Payment Request API for every customer HTML app.
- Customers who want stricter values than ours (a longer HSTS `max-age`,
  `preload`, a tighter `Referrer-Policy`) cannot set them either.

Custom domains are served by the platform's own edge without a CDN
(ADR-520), so no other layer sets or corrects these headers for them.

## Options

**A. Keep forced headers.** Simple and uniform. Leaves the four problems
above, and the HSTS one compounds over time as more custom domains are
visited.

**B. Platform defaults on customer responses, forced headers on Gregale's
own surfaces (proposed).** For responses from customer apps, use the app's
value when it sets the header (directly or through a `kind=headers` edge
rule) and fill in a platform default only when it does not. Keep today's
forced set on Gregale-owned surfaces (dashboard, API, apid).

**C. Per-app header policy setting.** A `security_headers` app setting with
`platform` (today) and `app` modes. More explicit but adds a setting nearly
every customer would leave on the default, and still needs B's defaults.

## Proposed decision (B)

| Header | Gregale surfaces | Customer app on `*.gregale.dev` | Customer app on a custom domain |
|---|---|---|---|
| `Strict-Transport-Security` | forced, unchanged | app value, else `max-age=31536000; includeSubDomains` | app value, else `max-age=31536000` (no `includeSubDomains`) |
| `X-Content-Type-Options` | forced | app value, else `nosniff` | app value, else `nosniff` |
| `Referrer-Policy` | forced | app value, else `strict-origin-when-cross-origin` | same |
| `X-Frame-Options` | forced `DENY` | app value only; no platform default | same |
| `Permissions-Policy` | forced | app value only; no platform default | same |

- `includeSubDomains` stays on `*.gregale.dev` app hosts because Gregale owns
  every name below them.
- Framing and browser-feature policy are application decisions; a platform
  default that silently breaks embeds or payments is the wrong default. Apps
  that want them set the header or add a `kind=headers` rule.
- No `preload` anywhere until the spec §11 policy review, as today.

### Implementation sketch

- `gatewayd-public` already distinguishes Gregale surfaces from customer
  routes (`isApidPath` and host resolution). For customer responses it stops
  dropping upstream copies of the five headers and applies a
  `httpsec.CustomerDefaults(host kind)` filler that only sets absent headers.
- `gatewayd-internal` stops pre-setting the platform values on customer
  responses, so the app's value is the only upstream copy.
- Tests: an app-set header survives both hops; an absent header gets the
  default for the host kind; Gregale surfaces stay byte-identical to today.
- Docs: replace the spec §11 table with the per-surface table above and
  document how to set each header from an app or a `kind=headers` rule.

## Consequences

- Customers can weaken a default (for example send no HSTS). That is
  their responsibility for their own origin; the defaults still cover the
  common case of an app that sets nothing.
- Browsers that already cached `includeSubDomains` for a custom domain keep
  it until `max-age` expires; this change stops new pinning, it cannot undo
  existing pins. Release notes should say so.
- Customer HTML apps lose the default `X-Frame-Options: DENY`; an app that
  relied on it without knowing becomes frameable. Release notes and the
  dashboard's security-headers preset should point to the one-line fix.

## Rejected alternatives

- **Drop only `includeSubDomains` on custom domains and keep everything
  forced.** Fixes the worst problem but leaves embeds and browser features
  broken and still prevents customers from tightening headers.
- **Remove all platform headers from customer responses.** Loses the
  harmless, broadly correct defaults (`nosniff`, HSTS without subdomains).
