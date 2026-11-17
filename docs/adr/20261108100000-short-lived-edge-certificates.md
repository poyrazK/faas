# ADR-20261108100000: Short-lived certificates for the apps domains

- **Status:** accepted
- **Date:** 2026-11-08
- **Amends:** ADR-20261103163954 §3 (the apps domains' certificate):
  6-day certificates instead of 90-day ones, renewed 4 days ahead instead of
  30.
- **Related:** ADR-20261107203000 and ADR-20261107220000 (a compromised
  compute node is the case to plan for)

## Context

Every compute edge serves the apps domains' wildcard certificate
(`*.gregale.dev`) straight to clients, so every compute node holds its
private key in memory. Root on any one node can copy it. With the key and a
DNS answer (a poisoned resolver, a hostile network, a route leak), the
attacker can present a valid certificate for any customer app.

Until now that key stayed good for up to 90 days. The key-loss runbook
(FaasEdgeCertificate.md) rotates the `edge-tls` identity and revokes the
certificate, but revocation only helps if clients check it: browsers mostly
don't, and Let's Encrypt has ended OCSP. So in practice a copied key stayed
usable until its certificate expired.

We considered:

- **One certificate per node.** Each node would get its own key, so a copied
  key would be one node's. But each of those certificates is still for
  `*.gregale.dev`, so it impersonates every app just the same. Let's
  Encrypt's limits (5 certificates for the same names in 7 days, 50 per
  registered domain per week) also cap it at a handful of nodes.
- **Keyless signing.** The key would stay on the control plane and edges
  would ask it to sign each handshake. That puts a control-plane round trip
  in every handshake and makes the control plane a dependency of serving.
  Much more to build.
- **Short-lived certificates.** Let's Encrypt's `shortlived` ACME profile
  issues certificates that live about 160 hours. A copied key is good for at
  most about 6.7 days, without depending on revocation. This only changes how
  edgednsd orders and renews.

## Decision

1. edgednsd orders with the ACME profile `api.EdgeCertificateProfile`
   (`shortlived`). The ACME client refuses a profile the CA's directory does
   not list.
2. It renews `api.EdgeCertificateRenewBefore` (4 days) before expiry, so a
   new certificate about every 64 hours. That is about 2.6 of the 5 allowed
   for the same names in 7 days, leaving two for rotating the key after a
   compromise.
3. An issued leaf that lives longer than `api.EdgeCertificateLifetimeMax`
   (7 days) is refused and counted as a failed order: if the CA ignores the
   profile, no long-lived key reaches the fleet.
4. A stored certificate that lives longer than 7 days is due at once, so the
   current 90-day certificate is replaced on the first check after deploy.
5. "Newest" means issued last (`not_before DESC, id DESC`), not expiring
   last, both for edgednsd's renewal decision and for what an edge serves.
   Otherwise the old 90-day certificate, which expires later, would stay
   newest: edges would keep serving its key and edgednsd would order every
   hour until Let's Encrypt refused.
6. Alerts: `FaasEdgeCertificateRenewalOverdue` fires when the newest stored
   certificate has under 3 days left (a day of failed orders), and
   `FaasEdgeCertificateExpiringSoon` when an edge serves one with under
   2 days left.

After a compromise, the response is still the key-loss runbook: rotate the
`edge-tls` identity so the node's copy cannot open new keys, and edgednsd
orders a new certificate. The copied key now dies within a week whether or
not anyone revokes it.

## Consequences

- A copied key impersonates apps for at most about 6.7 days, down from up to
  90.
- Less slack: if Let's Encrypt, Cloudflare DNS (DNS-01) or edgednsd is down
  for more than about 4 days, the apps domains' certificate expires and
  clients connecting directly fail their handshakes. Before, that slack was
  30 days. The alerts fire after one day of failed renewals. A second CA as
  a fallback is future work.
- About 2.6 orders a week instead of one every 60 days. The Certificate
  Transparency watch sees each one and recognises it from the table.
- `edge_tls_certificates` holds about 35 rows per CA (each kept 90 days past
  expiry), up from 2 or 3.
