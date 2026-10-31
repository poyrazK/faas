# FaasTLSCertExpiryPage

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metric: `gateway_tls_cert_expiry_seconds` (apid `/metrics`, `pkg/apid/certexpiry`).
Spec: §12, ADR-024 H3, ADR-520.
Severity: warn at 14 days (ADR-1236). `FaasTLSCertExpiryImminent` pages
below 72 hours and links here too.

## Symptom

The soonest-expiring edge certificate on a verified custom domain has under
14 days left (under 72 hours for `FaasTLSCertExpiryImminent`). Caddy on the
public edge obtains and renews every customer certificate on demand
(ADR-520) and renews at about 30 days, so renewal has been failing for at
least two weeks.

The value comes from apid's domain doctor, which completes a TLS handshake
with every verified custom domain at most every 30 s and records the leaf's
`NotAfter` in `custom_domains.cert_expires_at`. Every apid replica exports
the same value; the rule takes `min()`.

## Verify

Find the host from the per-host series, soonest first:

```bash
curl -fsS http://127.0.0.1:9101/metrics | grep gateway_tls_cert_expiry_by_host_seconds | sort -t' ' -k2 -n | head
```

Then confirm what the edge serves:

```bash
openssl s_client -connect <edge-ip>:443 -servername <host> </dev/null 2>/dev/null | openssl x509 -noout -enddate -issuer
```

## Fix

Renewal needs, for the host:

1. The `custom_domains` row still verified. A drifted or removed domain is
   not renewed on purpose; `gregale domains` shows its state.
2. The on-demand ask allowing it: check `gateway_tls_on_demand_*` on
   gatewayd-public and its log for a denial reason (plan, budget, unknown).
3. HTTP-01 reachable: port 80 open on the public edge and not redirected
   away for `/.well-known/acme-challenge/`.
4. CAA records on the customer's zone allowing Let's Encrypt.

The public edge's Caddy log (`journalctl -u caddy`) names the failing step
for the host. Once the cause is fixed, the next handshake to the host
triggers renewal; the doctor records the new expiry within a minute.
