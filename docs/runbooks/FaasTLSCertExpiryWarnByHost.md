# FaasTLSCertExpiryWarnByHost

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metric: `gateway_tls_cert_expiry_by_host_seconds{hostname,kind}` (gatewayd-public `/metrics`).
Spec: §12 + ADR-024 H3 follow-up (Finding 2).
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

Per-host cert expiry is between 14 and 30 days. certmagic normally
auto-renews at the 30-day mark, so a Warn here is a heads-up that
the auto-renew hasn't fired yet — most often because the renewal
queue is backed up behind a stuck previous renewal. If the gauge
doesn't climb back above 30 days within 12 hours, escalate to the
Page runbook (`FaasTLSCertExpiryPageByHost.md`).

## Triage

```bash
# What's currently in the 14-30d window?
curl -fsS http://127.0.0.1:9090/metrics \
  | grep '^gateway_tls_cert_expiry_by_host_seconds{' \
  | awk '$2 < 30 * 86400 && $2 >= 14 * 86400 {print}'
```

```bash
# Is certmagic's renew loop alive at all? (No frozen = no renew)
journalctl -u faas-gatewayd-public --since '-1h' --no-pager | grep -iE 'renewal|obtaining'
```

## Recover (if 12h pass without auto-renew)

```bash
# Force-renew for a specific host. Logs the result so you can see
# whether the DNS-01 / HTTP-01 path completed.
gregale domains verify "$HOST"

# If that doesn't bring the gauge above 30d within 5 minutes, fall
# back to:
systemctl restart faas-gatewayd-public
```

## Silence

```bash
amtool silence add \
  --matchers='alertname=FaasTLSCertExpiryWarnByHost,kind=ondemand' \
  --duration=24h \
  --comment='customer owns renewal cadence — let auto-renew retry'
```

## Notes

The Warn window (14-30d) is mostly informational. Operators
typically silence per-customer when the customer is known to be
managing their own custom-domain lifecycle (e.g. an enterprise
customer running cert-manager against a delegated CNAME), and only
act when the same host crosses into the Page window.

This rule complements (does not replace) the aggregate
`FaasTLSCertExpiryWarn` — the per-host attribution is the
actionable signal; the aggregate catches the box-level "soonest".
