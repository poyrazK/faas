# ADR 20261103172551 · Basic auth gets a wrong-guess budget; Cloudflare ingress needs the zone's client certificate

- **Status:** accepted.
- **Date:** 2026-11-03
- **Amends:** ADR-079 (per-app public auth, basic mode), the public_edge role
  (bug hunt 2026-10-27 H28, Authenticated Origin Pulls)
- **Related:** ADR-20261102081618 (compute nodes take app traffic; the zone's
  own origin-pull certificate), ADR-520 (customer-domain TLS),
  ADR-20261103090000 (edge security round 5)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

A sixth edge-security review found:

1. **Basic-auth passwords could be guessed at the edge's request rate.** A
   protected app in `basic` mode compared every presented password and
   answered 401, with no count of failures. Bearer mode refuses a client
   source after `GatewayFailedKeyLookupsPerSource` misses a minute; basic mode
   had nothing. The API accepted a one-byte password.
2. **The control plane's Caddy admitted every Cloudflare customer.** The
   platform site answers only Cloudflare's address ranges and loopback, but
   those ranges carry every Cloudflare customer's zone and Worker. Anyone
   could point a zone of their own at our origin and skip the platform zone's
   WAF, bot and rate-limit rules. `faas_public_edge_authenticated_origin_pulls`
   existed but was off by default, nothing turned it on, and its comment told
   the operator to install Cloudflare's shared origin-pull CA, which every
   Cloudflare zone's requests satisfy. The compute edges already require the
   zone's own client certificate (ADR-20261102081618).
3. **A platform site with no Cloudflare in front trusted `CF-Connecting-IP`.**
   With `faas_public_edge_cloudflare_only` off and no custom-domain TLS,
   nothing aborts direct clients, yet the site still copied the client-sent
   `CF-Connecting-IP` into `X-Forwarded-For`. A client chose the address that
   ip, geo, allowlist and throttle rules see.

## Decision

1. **A wrong-guess budget per client and app.** Bug hunt H76 (#6498) landed
   this first, and this record adopts it: one client prefix (IPv4 /24 or
   IPv6 /64) may present `PublicAuthBasicFailuresPerWindow` (10) wrong
   passwords to one app per `PublicAuthBasicFailureWindow` (one minute). Past
   it the gateway answers 429 `auth_rate_limited` with `Retry-After`, without
   comparing. The budget is per app, so a lockout on one app does not reach
   another. A newly set basic-auth password is at least
   `PublicAuthBasicPassMinBytes` (12) bytes; existing credentials keep working
   until they are next changed.
2. **Cloudflare ingress requires the zone's client certificate.** The
   public_edge role refuses to deploy a platform site that admits Cloudflare
   (`faas_public_edge_cloudflare_only`, or custom-domain TLS) without
   `faas_public_edge_authenticated_origin_pulls`, and refuses an origin-pull
   CA whose subject or issuer is Cloudflare's shared
   `origin-pull.cloudflare.net`. The CA to install is the zone's own
   (`origin-pull-ca.pem` from `deploy/scripts/cloudflare-origin-pull.sh`), the
   one the compute edges already trust. An operator who must deploy without
   it, for example during the certificate rollout, sets
   `faas_public_edge_allow_any_cloudflare_zone: true`. The manifest picks
   one: `dns.origin_pull: required` renders the certificate check,
   `dns.origin_pull: any_zone` the opt-out; unset renders neither.
3. **No Cloudflare, no `CF-Connecting-IP`.** Without the Cloudflare-only
   abort, the platform site's hop is the peer address and a client-sent
   `CF-Connecting-IP` is removed, as on the customer-domain site.

## Consequences

- The next control-plane deploy fails until the operator installs the
  zone's origin-pull CA at `faas_public_edge_origin_pull_ca`, turns on
  zone-level Authenticated Origin Pulls (docs/runbooks/EdgeDirectEntry.md
  steps 1 and 3) and sets `dns.origin_pull: required` in the manifest, or
  sets `dns.origin_pull: any_zone`. Every zone the platform site serves (the platform, apps,
  deployment-preview and alias domains) needs the client certificate
  uploaded, signed by the same CA.
- A client that mistypes a basic-auth password ten times in a minute waits
  until the failures age out. Clients in one /24 (IPv4) or /64 (IPv6) share
  the budget per app.
- A request to set a basic-auth password shorter than 12 bytes gets 422.
