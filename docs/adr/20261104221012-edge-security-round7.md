# ADR-20261104221012: Edge security round 7 — vouched proxy exemption, cross-origin Basic auth, raw ingress gates, active uploads

- Status: accepted
- Date: 2026-11-04
- Amends: ADR-20261103163954 (app hosts point straight at the nodes), ADR-831 (edge WAF),
  ADR-079/118/119 (per-app public auth), the object storage public-read and upload-route designs

## Context

A review of the public edge after round 6 (#6490) found:

1. **The Cloudflare-range exemption is claimable by anyone on Cloudflare.**
   With `front: none` the edge exempts Cloudflare's address ranges from the
   per-address connection limits, and the nftables entry backstop skips them
   entirely. Every Cloudflare customer's zone (and Workers) connects from those
   ranges, so any of them could point a hostname at a compute node and open
   unlimited connections and handshakes, bypassing both layers.
2. **Basic auth is replayable cross-origin.** A browser that has typed an app's
   Basic credentials attaches them to every request to that origin, including
   form posts and WebSocket handshakes started by another site. Every app host
   is same-site under the shared apps domain, so SameSite gives nothing.
3. **Raw TCP/UDP ingress skips the gateway's gates.** `tcpd` and `udpd`
   resolved and admitted sessions for apps whose account was suspended,
   deletion-pending or abuse-held, whose live deployment was security
   quarantined, or whose public auth was `internal_only` or `ip_allowlist`.
4. **Uploads and public buckets serve active content on the app's origin.**
   An upload route with no allowlist (or `image/*`) accepted `text/html` and
   `image/svg+xml`; a public bucket mount served them on the app's hostname, so
   any user with an upload key could plant script that runs on the app's
   origin (stored XSS against the app and its cookies).
5. **Public bucket paths are outside edge rules and app access control.** That
   is by design (they are served before the gateway, without waking the app),
   but it was undocumented.

## Decision

1. **The proxy exemption is provisional until vouched.** A connection from a
   proxy range is accepted without counting, but after the TLS handshake the
   edge settles it (`clientlimit.Listener.Settle`): if the client presented a
   certificate that verified against the zone's origin-pull CA, it stays exempt;
   otherwise it is counted against its address like any other client and reset
   when that address is over its limits. The nftables backstop no longer skips
   proxy ranges: they get their own rules at `PublicEntryProxyFactor` (16×) the
   per-source limits (`PublicEntryProxy*` in `pkg/api/limits.go`), high enough
   for a few Cloudflare addresses carrying many clients, low enough that one
   source cannot take the whole node.
2. **Basic auth refuses cross-origin browser requests.** For an app in
   `basic` mode the gateway refuses with `403` (audited as
   `instances.public_auth_cross_origin`) a request whose `Origin` names a
   different host, when the request is unsafe (not GET/HEAD/OPTIONS) or a
   WebSocket handshake. `Origin: null`, several origins or an unparsable one
   are refused the same way. A request with no `Origin` (curl, servers) and
   same-host requests pass. An app opts a partner origin in with a `cors`
   edge rule of its own account that lists the origin explicitly (not `*`) with
   `allow_credentials: true`.
3. **Raw ingress meets the gateway's access gates.** Main now refuses an
   inactive account owner (`RequireActiveOwner`, #6644) and a quarantined
   production deployment (`RequireDeployment`, ADR-20261105115100) on raw
   ingress. `pkg/ingressroute.CheckApp` adds the app's public access setting
   in both resolvers: it refuses an app whose public auth is `internal_only`,
   or whose `ip_allowlist` does not contain the session's peer address (an
   unknown peer is refused). Basic and bearer have no meaning on a raw socket
   and are not applied; an app needing them on TCP must authenticate in its
   own protocol. The spend cap is already enforced at schedd admission.
4. **Active content is never served from a public mount.** An active type
   (HTML, XHTML, SVG, XML and `+xml`, XSL, JavaScript,
   `multipart/x-mixed-replace`) matches an upload route only when the route
   lists it exactly; a wildcard or an empty allowlist never admits it. A route
   on a public served bucket cannot list one at all (`400`), and the edge
   refuses such an upload with `415`. Public reads of SVG and XML (objects put
   by other means) carry `Content-Security-Policy: sandbox; default-src
   'none'; …`, so opened directly they cannot run script.
5. **Bucket paths stay outside edge rules and app access control**, and the
   object storage and edge rules docs now say so.

## Not decided

- **The wildcard key on every compute node.** With direct entry each compute
  node holds the apps-domain wildcard key (now ordered by edgednsd and sealed to
  the edges, #6555/#6564). A compromised node can impersonate every app until
  the certificate is revoked. Options are per-node certificates for the hosts a
  node serves, keyless signing from the control plane, or accepting the risk
  with short-lived certificates. Owner decision.
- `DecodeJSON` ignores `Content-Type`. It is not exploitable today because
  cookie writes already require a same-origin `Origin`/`Sec-Fetch-Site`, so it
  is left as is.

## Consequences

- A Cloudflare zone other than ours gains nothing from connecting from
  Cloudflare's ranges; our own zone keeps its exemption.
- Browser apps on Basic auth that post from another origin must add a `cors`
  edge rule naming that origin with credentials.
- TCP/UDP apps behind `ip_allowlist` now actually enforce it; an app set to
  `internal_only` no longer takes raw public sessions.
- An existing upload route on a public bucket keeps its stored allowlist, but
  the edge refuses active uploads through it.
