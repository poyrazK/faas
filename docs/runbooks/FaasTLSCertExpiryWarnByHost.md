# FaasTLSCertExpiryWarnByHost

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metric: `gateway_tls_cert_expiry_by_host_seconds{hostname,kind}` (apid `/metrics`, `pkg/apid/certexpiry`).
Spec: §12, ADR-024 H3 follow-up, ADR-520.
Severity: warn.

## Symptom

The edge certificate for the host has between 14 and 30 days left. Caddy
renews at about 30 days, so a renewal for this host has just started
failing. apid's domain doctor is the source (see FaasTLSCertExpiryPageByHost).

## Verify

Check `journalctl -u caddy` on the public edge for renewal attempts for the
host, and the gatewayd-public log for an on-demand ask denial.

## Fix

Follow the Fix section of `FaasTLSCertExpiryPage.md` for this host. If the
Caddy log shows no renewal attempt within 12 hours, a handshake to the host
(`curl -sI https://<host>`) prompts one.
