# Plain HTTP on the public entry addresses redirects to HTTPS

Date: 2026-11-27
Status: Accepted
Amends: ADR-20261103163954 §2 (public entry carries 443 only), §4 (per-source limits)

## Context

With the edge's direct entry (ADR-20261103163954), `*.gregale.app` resolves
straight to the compute nodes' public entry addresses; no proxy stands in
front. Those addresses carried 443 only: the host firewall dropped everything
else, so `http://<app>.gregale.app/` and a custom domain's `http://` URL hung
until the client gave up (prod hunt r74, 2026-11-27). Platform hosts behind
Cloudflare answer port 80 with a 308; app hosts did not. A typed or old
`http://` link, a webhook sender configured with `http://`, and a health
checker all saw a timeout instead of a redirect.

## Decision

1. gatewayd-public opens a plain-HTTP listener when
   `FAAS_EDGE_PUBLIC_HTTP_REDIRECT_ADDR` is set (the edge role's drop-in sets
   `:80` on direct, no-front edges; `faas_edge_http_redirect: false` turns it
   off). It needs the direct TLS listener and refuses to start otherwise.
2. It answers every request with a **308 Permanent Redirect** to
   `https://<host><path>?<query>`: 308 keeps the method and body, so a POST is
   not replayed as a GET. It never proxies, reads a body, sets a cookie or
   looks up a route; an IP literal or platform host gets 404 and a missing
   Host 400. Read, write and header timeouts are 5 s, idle 30 s, and the
   header limit is 16 KiB.
3. The host firewall accepts 80 alongside 443 on each entry address, under
   the same per-source rules: one `tcp dport { 80, 443 }` match feeds the
   existing `public_entry_rate_*` and `public_entry_conns_*` sets, so port 80
   does not double a source's budget. Everything else on the entry addresses
   is still dropped.
4. The socket is shared with a hand-off successor like the edge's other
   listeners; both generations may answer redirects while they overlap.

## Consequences

- `http://` app URLs answer at once with a redirect browsers and HTTP clients
  follow. HSTS (already sent on HTTPS responses) keeps returning browsers
  from asking over HTTP again.
- Port 80 is a new listener on the public internet. It parses one request
  head and writes a fixed response; the kernel backstop and the shared
  per-source sets bound what a single source can open.
- No HTTP-01 ACME: the apps certificate comes from the database (DNS-01).
- Deploy order: the nftables change and the gatewayd-public drop-in ship in
  the same edge converge; a node with the new firewall but an old binary
  refuses port 80 at the socket instead of hanging.
