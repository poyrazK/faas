# ADR-520 · Self-hosted TLS for customer custom domains

- **Status:** proposed
- **Date:** 2026-10-03
- **Amends:** spec §4.1 ("TLS and domains"), ADR-024 (custom-domain issuance
  path), ADR-167 (wildcard certificate issuance).
- **Decision:** Customer custom domains get certificates from the platform's
  own public edge, with no CDN in their path. The control plane's Caddy uses
  on-demand TLS for every hostname without a static site: before it loads a
  certificate from storage, obtains one, or renews one, it calls
  `GET http://127.0.0.1:9092/v1/internal/tls/ask?domain=<host>` on
  gatewayd-public's loopback control listener and proceeds only on 200.
  gatewayd-public answers 200 for a verified exact custom domain, or for a
  host below a verified wildcard custom domain that has no exact row of its
  own and fits the wildcard's issuance budget. Customers point their domain
  at a DNS-only target hostname (`FAAS_CUSTOM_DOMAIN_TARGET`, for example
  `edge.gregale.dev`) or, at a zone apex, at the edge's addresses
  (`FAAS_CUSTOM_DOMAIN_ADDRESSES`). The feature is off unless the deploy
  manifest sets `public_edge.custom_domains.mode: on_demand`.

## Context

Domain management (add, TXT ownership proof, verify, doctor, wildcards,
environment-scoped domains, certificate status and webhooks) shipped, but no
deployed path issued a certificate for a customer hostname:

- Production terminates TLS at Caddy with a Cloudflare Origin CA
  certificate for `gregale.dev, *.gregale.dev` only, and the host and
  provider firewalls admit 80/443 from Cloudflare only.
- gatewayd-internal's certmagic engine needs `FAAS_TLS_STORAGE_DIR` and
  `FAAS_TLS_CONTACT_EMAIL`, which no deploy path sets. Even when wired it
  solves DNS-01 by writing TXT records with the operator's own DNS token,
  which cannot reach a customer's zone. Its certificates are written to disk
  and served by nothing (PR #633 removed TLS from gatewayd-public).
- The CNAME target customers were told to use, the apps-domain apex, is
  proxied by Cloudflare; a customer hostname resolving there reaches
  Cloudflare, which does not serve it.

The platform must not depend on Cloudflare for customer domains.

## Decision details

- **Terminator.** Caddy stays the TLS boundary (spec §4.1, ADR-070);
  gatewayd-public stays plain HTTP. Caddy's on-demand TLS with an `ask`
  endpoint is the smallest change that issues per hostname, renews, staples
  OCSP and retries with back-off, using HTTP-01 or TLS-ALPN-01 against the
  edge itself. The configuration is compatible with Caddy 2.6 (Ubuntu
  24.04's package).
- **Permission check** (`pkg/gateway/ondemand_tls_ask.go`). The decision
  reads the same rows routing reads: `DomainByName`, then
  `WildcardDomainForHost`. An unverified exact row denies even below a
  verified wildcard, matching routing precedence. Names in or below the apps
  domain are refused; the edge serves them from static certificates.
  Tenant-surface hostnames (ADR-100) are not admitted yet. Store errors deny.
  Only loopback peers are answered. Caddy asks on every handshake whose
  server name has no certificate in memory, so with 443 open to everyone the
  endpoint bounds its store lookups (`OnDemandTLSAskLookupsPerSecond`, burst
  `OnDemandTLSAskLookupBurst`; past it the answer is `deny_overload` and the
  next handshake asks again) and negative-caches names with no custom-domain
  row for `OnDemandTLSAskNegativeCacheSeconds`. Every decision is counted in
  `gateway_tls_on_demand_ask_total{decision}`.
- **Wildcard budget.** Caddy asks again before every reload and renewal
  (certmagic checks the decision before loading from storage), so the budget
  cannot be an in-memory rate limit: each gatewayd-public restart would
  refuse certificates that already exist. `custom_domain_tls_hosts` records
  each admitted host below a wildcard. A known host is admitted for free; a
  new host is admitted only while fewer than
  `api.OnDemandTLSWildcardNewHostsPerWeek` (40) hosts were newly admitted for
  that wildcard in the last 7 days. 40 stays under Let's Encrypt's
  50 certificates per registered domain per week and caps how many orders one
  wildcard can draw from the shared ACME account. gatewayd-public is the only
  writer. The rows are edge runtime state, not customer intent, and cascade
  away with the wildcard.
- **Certificate status.** In on-demand mode apid owns custom-domain
  certificate status through its existing port-443 probe, and stops emitting
  `domain_verify`, so gatewayd-internal's DNS-01 wildcard minter does not
  also write `failed: cert engine unwired`. Right after verification apid
  probes immediately; that handshake is what asks the edge to issue. A failed
  probe within `api.OnDemandTLSIssuanceGraceSeconds` (15 min) of
  verification reports `pending`, not `failed`, so ACME validation that
  outlasts the 5 s probe does not trigger the failure email.
- **Routing target.** The doctor's `points_to_gregale` check accepts the
  configured target, or A/AAAA answers that all belong to the configured edge
  addresses. That also fixes apex domains, for which Go reports the queried
  name as its own canonical name. A configured target replaces the
  apps-domain apex as the expected CNAME, so a domain still pointing at the
  proxied apex is reported (and, if verified, drifted) until fixed; those
  domains could not get a certificate before either. API responses carry
  `dns_records` (TXT proof, CNAME, A/AAAA alternatives) and the CLI prints
  them.
- **Network.** Enabling the feature opens 80/443 to every source in
  nftables. The platform site keeps answering Cloudflare only: Caddy aborts
  connections to `gregale.dev`/`*.gregale.dev` from any other peer, and the
  Host-based route check means a direct client cannot reach a platform host
  by sending a customer SNI. On customer hosts the client IP is the TCP peer;
  a client-sent `CF-Connecting-IP` is never trusted there.

## Consequences

- A customer domain works end to end without Cloudflare: verify the TXT
  proof, point the name at the target, and the first handshake issues the
  certificate. Renewal is automatic.
- The control plane's public address becomes directly reachable on 80/443.
  Platform hosts stay Cloudflare-only at Caddy, not at the packet filter;
  other site blocks appended to the Caddyfile (for example
  `s3.gregale.dev`) become directly reachable. The provider firewall must
  admit 80/443 from everywhere for the control plane, which the public-beta
  GCP audit (`deploy/gcp/public-beta-policy.json`) forbids; a deployment
  that enables this ADR must update that policy deliberately.
- Certificates live in Caddy's storage on the single control-plane edge.
  More than one edge needs shared Caddy storage before it can serve customer
  domains; until then a second edge would race the first for ACME orders.
- Wildcard custom domains get one certificate per subdomain, at most 40 new
  subdomains per wildcard per week. SaaS-scale wildcards need a delegated
  DNS-01 path (`_acme-challenge` CNAME to a platform zone) and a real
  wildcard certificate; that is a follow-up.
- gatewayd-internal's DNS-01 engine is unchanged and still used when on-demand
  mode is off. Tenant surfaces keep their own certificate state machine.

## Rejected alternatives

- **Cloudflare for SaaS custom hostnames.** Keeps the edge Cloudflare-only
  but makes customer domains depend on Cloudflare, which is what this ADR
  removes.
- **Bring TLS back into gatewayd-public with certmagic.** Possible (the
  allowlist and issuer code exist) and would let an issuer wrapper meter real
  issuances only, but it re-merges the TLS edge that ADR-070/PR #633 split
  out, needs shared certificate storage in Postgres, and duplicates what Caddy
  already runs in production.
- **In-memory rate limit in the ask endpoint** (or Caddy 2.6's
  `on_demand_tls interval/burst`). Both also gate reloads and renewals, so a
  restart would deny existing certificates. Caddy deprecated the latter for
  that reason.
- **DNS-01 with the operator's DNS token.** Cannot write to a customer zone.
