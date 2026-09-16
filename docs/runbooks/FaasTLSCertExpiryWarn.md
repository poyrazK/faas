# FaasTLSCertExpiryWarn

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metric: `gateway_tls_cert_expiry_seconds` (gatewayd-public `/metrics`).
Spec: §12 + ADR-024 H3 (closed in PR #345).
Severity: warn.

> **Legacy daemon only (revised 2026-08-04).** This runbook applies
> to the legacy `cmd/gatewayd/` daemon and to `cmd/gatewayd-public/`
> *before* PR #633. The production deployment terminates TLS at
> Caddy + Cloudflare upstream of `api.gregale.dev`, not in the
> daemon. PR #633 stripped certmagic + Hetzner DNS-01 from
> `gatewayd-public`; the legacy daemon keeps them during the
> migration window. PR-C sweeps the certmagic packages and
> this runbook will be archived alongside them.
>
> The certificate metrics and this runbook are retained for legacy
> observability only. Current production TLS terminates at the upstream
> Caddy/Cloudflare edge; use `docs/ops/secrets-rotation.md` for the current
> provider-token procedure. The legacy `gatewayd.toml` config file is kept
> only as migration reference.

## Symptom

Smallest remaining lifetime across cached certs in `cfg.StorageDir`
is between 14 and 30 days. Certmagic's renew loop normally starts
at 30 d; a cert sitting at 14 d means renewals fired but failed,
or the cert on disk is not the one certmagic is tracking
(certmagic version mismatch, or the apps domain was moved to a
new wildcard).

## Verify

```bash
curl -fsS http://127.0.0.1:9090/metrics | grep gateway_tls_cert_expiry_seconds
journalctl -u faas-gatewayd-public --since '-7d' --no-pager | grep -iE 'renew|certmagic'
# What's certmagic's view of the cert? The simplest check is the
# on-disk NotAfter (the gauge's source of truth).
for c in /var/lib/faas/certs/certificates/*/*/; do
  openssl x509 -enddate -noout -in "$c/$(basename "$c").crt" 2>/dev/null
done
```

## Check

This is the "renewal worked but its result wasn't picked up" state.
Common causes:

- **certmagic version drift**: a Firecracker upgrade or a go.mod
  bump landed an incompatible certmagic. The on-disk cert is fine
  but certmagic won't renew it (the cache thinks it already has
  a valid one for the apex).
- **Hetzner token read-only**: the token has zone-write but the
  DNS-01 challenge TXTs are getting SERVFAIL because the Hetzner
  authoritative servers are slow. Look for `acme: error presenting
  challenge` in the gatewayd-public log.

```bash
# Cross-check: did the wildcard cert get re-minted in the last 24h?
find /var/lib/faas/certs/certificates -name '*.crt' -mtime -1 -ls

# Cross-check: is certmagic's renewal loop alive?
journalctl -u faas-gatewayd-public --since '-1h' --no-pager | grep -c 'renew'
```

## Silence

```bash
amtool silence add \
  --matchers='alertname=FaasTLSCertExpiryWarn' \
  --duration=6h \
  --comment='renewal window — investigating'
```

## Recover

The page-tier runbook (`FaasTLSCertExpiryPage.md`) covers the
recovery path. The warn-tier is a "you have a few days" signal;
investigate the renewal chain before it tips into page territory.
