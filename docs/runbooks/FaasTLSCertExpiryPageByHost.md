# FaasTLSCertExpiryPageByHost

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metric: `gateway_tls_cert_expiry_by_host_seconds{hostname,kind}` (apid `/metrics`, `pkg/apid/certexpiry`).
Spec: §12, ADR-024 H3 follow-up, ADR-520.
Severity: warn (ADR-1236). `FaasTLSCertExpiryImminent` pages below 72 hours.

## Symptom

The edge certificate for the alert's host has under 14 days left. apid exports
the soonest-expiring verified custom domains (at most
`CustomDomainCertExpiryExportMaxHosts`) from the expiries its domain doctor
records; `kind` is `ondemand`, because the edge obtains every customer
certificate on demand. A wildcard row's hostname is the wildcard itself
(`*.example.com`); the doctor measures it through
`gregale-wildcard-check.example.com`.

## Verify

```bash
openssl s_client -connect <edge-ip>:443 -servername <host> </dev/null 2>/dev/null | openssl x509 -noout -enddate -issuer
```

## Fix

Follow the Fix section of `FaasTLSCertExpiryPage.md` for this host: verified
row, on-demand ask decision, HTTP-01 on port 80, CAA allowing Let's Encrypt.
