# FaasEdgeCertificate: the apps domains' certificate

Decision: ADR-20261103163954 §3. Source:
`deploy/ansible/roles/prometheus/files/faas.rules.yml`.

`edgednsd` on the control plane orders the apps domains' certificate (for
example `*.gregale.dev`) from Let's Encrypt with the DNS-01 challenge and
renews it 30 days before it expires. Each certificate gets a new ECDSA P-256
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
  `edge_certificate_acme_account_info{ca,account_uri}`.
- each edge: `edge_certificate_expiry_seconds{name}`,
  `edge_certificate_loads_total{result}`,
  `faas_seal_domain_loaded{domain="edge-tls"}`.

Logs: `journalctl -u faas-edgednsd | grep edgecert` on the control plane,
`journalctl -u faas-gatewayd-public | grep edgecert` on a compute node.

## FaasEdgeCertificateRenewalOverdue (warn)

The newest stored certificate expires within 21 days: edgednsd's orders have
been failing for about a week. Find why:

```bash
journalctl -u faas-edgednsd --since -2d | grep 'edgecert: certificate order failed'
curl -s 127.0.0.1:9113/metrics | grep -E 'edge_certificate_(orders_total|stored_expiry)'
```

Usual causes:

- **The DNS-01 record does not land.** The token lost Zone > DNS: Edit, or
  `FAAS_EDGEDNS_CERT_ZONE_ID` names another zone. `edge_dns_errors_total`
  rises too.
- **CAA refuses the order.** The zone's CAA records name another CA or
  account. Compare them with `edge_certificate_acme_account_info`:
  `dig +short CAA <apps domain>`.
- **Let's Encrypt limits.** After five failed validations in an hour the
  account waits; edgednsd already backs off from 1 h to 12 h. Do not restart
  edgednsd in a loop: each start retries at once.
- **The edge-tls recipient is missing** after a seal_domains change; edgednsd
  then refuses to start (`the edge-tls recipient`).

Once the cause is fixed, restart edgednsd to order at once:
`sudo systemctl restart faas-edgednsd`. The edges load the new certificate on
its notification.

## FaasEdgeCertificateExpiringSoon (warn)

An edge serves a certificate with under 14 days left. Either renewal is
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

## A lost or exposed key

Every compute node holds the key in memory. If a node is compromised, revoke
the certificate and order a new one before the attacker can use it:

1. Rotate the `edge-tls` domain (`gregalectl seal-domain init --domain
   edge-tls --force`, then the seal_domains role), so the old identity no
   longer opens new keys.
2. Restart edgednsd: the recipient changed, so it orders a certificate with a
   new key; the edges load it on its notification.
3. Revoke the old certificate with the serial from `edge_tls_certificates`
   (`certbot revoke --cert-path` with the chain, or the ACME account's
   revocation; the account key is `/etc/faas/secrets/edgednsd/acme-account.key`).
