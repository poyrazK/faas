# FaasTLSCertExpiryWarn

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metric: `gateway_tls_cert_expiry_seconds` (apid `/metrics`, `pkg/apid/certexpiry`).
Spec: §12, ADR-024 H3, ADR-520.
Severity: warn.

## Symptom

The soonest-expiring edge certificate on a verified custom domain has between
14 and 30 days left. Caddy renews on-demand certificates at about 30 days,
so this is usually a renewal that has just started failing. apid's domain
doctor is the source (see FaasTLSCertExpiryPage).

## Verify

```bash
curl -fsS http://127.0.0.1:9101/metrics | grep gateway_tls_cert_expiry_by_host_seconds | sort -t' ' -k2 -n | head
```

The lowest value names the host; `journalctl -u caddy` on the public edge
shows whether a renewal for it was attempted and why it failed.

## Fix

Follow the Fix section of `FaasTLSCertExpiryPage.md`: verified row, ask
decision, HTTP-01 on port 80, CAA. Nothing needs doing if Caddy renews the
host within a day; the alert clears when the doctor records the new expiry.
