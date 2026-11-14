# FaasEdgeCertificate: the apps domains' certificate

Decision: ADR-20261103163954 §3, ADR-20261108100000. Source:
`deploy/ansible/roles/prometheus/files/faas.rules.yml`.

`edgednsd` on the control plane orders the apps domains' certificate (for
example `*.gregale.dev`) from Let's Encrypt with the DNS-01 challenge,
under the `shortlived` profile (about 160 hours), and renews it 4 days
before it expires: a new certificate about every 64 hours. Each certificate gets a new ECDSA P-256
key, sealed to the `edge-tls` seal domain and stored in
`edge_tls_certificates`; `edgednsd` cannot read it back. Each compute edge's
`gatewayd-public` (with `FAAS_EDGE_PUBLIC_TLS_CERT_SOURCE=database`) loads
the newest valid certificate at start, on the `edge_tls_certificate`
notification and every 10 minutes, opens the key with the `edge-tls`
identity and keeps it in memory only. A failed load keeps the certificate it
serves. An edge with no certificate is not ready, so edgednsd sends no client
to it.

Series:

- edgednsd: `edge_certificate_orders_total{ca,result}`,
  `edge_certificate_stored_expiry_timestamp_seconds{ca}`,
  `edge_certificate_acme_account_info{ca,account_uri}`; with the
  Certificate Transparency watch on,
  `edge_certificate_transparency_unexpected`,
  `edge_certificate_transparency_checks_total{result}` and
  `edge_certificate_transparency_last_success_timestamp_seconds`.
- each edge: `edge_certificate_expiry_seconds{name}`,
  `edge_certificate_loads_total{result}`,
  `faas_seal_domain_loaded{domain="edge-tls"}`.

Logs: `journalctl -u faas-edgednsd | grep edgecert` on the control plane,
`journalctl -u faas-gatewayd-public | grep edgecert` on a compute node.

## FaasEdgeCertificateRenewalOverdue (warn)

The newest stored certificate expires within 3 days: edgednsd's orders have
been failing for about a day. Edges fail every direct handshake when it
expires, so this is urgent. Find why:

```bash
journalctl -u faas-edgednsd --since -2d | grep 'edgecert: certificate order failed'
curl -s 127.0.0.1:9113/metrics | grep -E 'edge_certificate_(orders_total|stored_expiry)'
```

Usual causes:

- **The DNS-01 record does not land.** The token lost Zone > DNS: Edit, or
  `FAAS_EDGEDNS_CERT_ZONE_ID` names another zone. `edge_dns_errors_total`
  rises too.
- **CAA refuses the order.** Gregale publishes no CAA records for the apps
  domain (ADR-20261103163954 §3), so someone added one that does not allow
  `letsencrypt.org`: `dig +short CAA <apps domain>`. Remove it, or allow
  `letsencrypt.org`.
- **Let's Encrypt limits.** After five failed validations in an hour the
  account waits; edgednsd already backs off from 1 h to 12 h. Do not restart
  edgednsd in a loop: each start retries at once.
- **The edge-tls recipient is missing** after a seal_domains change; edgednsd
  then refuses to start (`the edge-tls recipient`).
- **`longer than` in the failure:** the CA issued a certificate without the
  `shortlived` profile and edgednsd refused it. **`unknown profile`:** the
  CA's directory no longer lists the profile. Either way the CA changed;
  check Let's Encrypt's status page and the ACME directory's `meta.profiles`
  (`curl -s https://acme-v02.api.letsencrypt.org/directory`).
- **Rate limit (`too many certificates`):** more than 5 orders for the same
  names in 7 days, usually from repeated key rotations. Wait for the
  oldest of those orders to age out of the window.

Once the cause is fixed, restart edgednsd to order at once:
`sudo systemctl restart faas-edgednsd`. The edges load the new certificate on
its notification.

## FaasEdgeCertificateExpiringSoon (warn)

An edge serves a certificate with under 2 days left. Either renewal is
failing (the alert above fired first), or this edge has not loaded the
renewed certificate:

```bash
# on the control plane: the newest stored certificate
sudo -u faas psql -Atc "select id, names, ca, not_after from edge_tls_certificates order by not_after desc limit 3"
# on the compute node
curl -s 127.0.0.1:9109/metrics | grep -E 'edge_certificate_(expiry_seconds|loads_total)|faas_seal_domain_loaded'
journalctl -u faas-gatewayd-public --since -1h | grep edgecert
```

- `loads_total{result="kept"}` rising with `could not open`: the edge's
  `edge-tls` identity does not open the new key. The recipient edgednsd seals
  to and the identity on the node must be the same pair; re-stage the domain
  with the seal_domains role. A new recipient makes edgednsd order a new
  certificate on its next check.
- `kept` with a database error: the node cannot reach the control plane's
  Postgres. The edge keeps serving its certificate until it expires; fix the
  connection.
- `FAAS_EDGE_PUBLIC_TLS_CERT_CA` differs from edgednsd's
  `FAAS_EDGEDNS_ACME_CA`: the edge only serves certificates from its CA.

A restart of `faas-gatewayd-public` reloads at once.

## FaasEdgeCertificateUnexpected (page)

edgednsd's Certificate Transparency watch
(`public_edge.dns.certificate.transparency` in the manifest) reads every
unexpired publicly trusted certificate for the apps domain and the names
under it from Cert Spotter every 6 hours. A certificate is edgednsd's when
its key is one it stored in `edge_tls_certificates`; one whose every name is
in `allow_names` (the CDN's own `gregale.dev` + `*.gregale.dev` certificate, a
hosted site such as `operations.gregale.dev`) is someone else's by design.
Anything else pages.

```bash
journalctl -u faas-edgednsd --since -1d | grep 'a certificate edgednsd did not issue'
# the issuance, from the cert_sha256 in that line
curl -s "https://api.certspotter.com/v1/issuances?domain=<apps domain>&include_subdomains=true&expand=dns_names&expand=issuer" | jq '.[] | select(.cert_sha256=="<cert_sha256>")'
```

Decide what it is:

- **A single app host** (`h1.gregale.dev`) from any CA: what a compromised
  compute node could get by answering a CA's TLS-ALPN challenge for a host it
  serves. Treat the node as compromised
  ([compute-node-compromise.md](compute-node-compromise.md)): follow "A lost
  or exposed key" below, and revoke that certificate too (its CA's problem-reporting address
  is in the issuance's `problem_reporting`).
- **A wildcard** or the apex: needs control of the zone's DNS. Check
  Cloudflare's audit log for TXT records edgednsd did not write, rotate the
  edgednsd token, then revoke.
- **Expected after all** (a new hosted site, the CDN switching CA with
  new names): add its names to `allow_names` and converge the control
  plane. The page clears on the next check.

The page also clears once the certificate expires out of the logs.

## FaasEdgeCertificateTransparencyStale (warn)

The watch has not completed a check for a day.
`edge_certificate_transparency_checks_total{result="rate_limited"}` rising:
without an API key Cert Spotter allows a few queries a day. Stage a key from
an SSLMate account at `/etc/faas/secrets/edgednsd/certspotter-api-key` (root,
0400) and converge the control plane; the role passes it as a credential.
`result="error"`: edgednsd's log names the failure (Cert Spotter down, the
control plane's outbound HTTPS, or Postgres for the stored keys).

## A lost or exposed key

Every compute node holds the key in memory, so a compromised node may have
copied it. That copy is good until the certificate expires, at most about
6.7 days (ADR-20261108100000). Browsers mostly don't check revocation, so
cut the window short by replacing the key:

1. Rotate the `edge-tls` domain (`gregalectl seal-domain init --domain
   edge-tls --force`, then the seal_domains role), so the old identity no
   longer opens new keys.
2. Restart edgednsd: the recipient changed, so it orders a certificate with a
   new key; the edges load it on its notification.
3. Revoke the old certificate with the serial from `edge_tls_certificates`
   (`certbot revoke --cert-path` with the chain, or the ACME account's
   revocation; the account key is `/etc/faas/secrets/edgednsd/acme-account.key`).
