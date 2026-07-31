# FaasTLSCertExpiryPageByHost

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metric: `gateway_tls_cert_expiry_by_host_seconds{hostname,kind}` (gatewayd `/metrics`).
Spec: §12 + ADR-024 H3 follow-up (Finding 2 of the gatewayd gap
analysis; PR in `worktree-tier1-gatewayd-fix`).
Severity: page.

## Symptom

Per-host cert expiry is ≤ 14 days. The aggregate
`FaasTLSCertExpiryPage` rule fires on the same condition — this rule
attaches the `hostname` + `kind` labels so the alert routes to the
specific custom domain (or wildcard) that's affected. `kind` is
either `wildcard` (DNS-01, the `*.apps.<zone>` cert) or `ondemand`
(HTTP-01, customer custom domains).

The bounded-admission overflow (`hostname="__other__"`) and
misclassifications (`kind="unknown"`) are excluded from the rule so
they never page — those conditions are operator-actionable
diagnostics, not customer-facing alerts.

## Triage by kind

```bash
# What's currently low across the fleet?
curl -fsS http://127.0.0.1:9090/metrics \
  | grep '^gateway_tls_cert_expiry_by_host_seconds{' \
  | awk '$2 < 14 * 86400 {print}'
```

### kind=wildcard

A single wildcard cert covers `*.apps.<zone>`. Renewals are
DNS-01-driven and run automatically; failure modes are:

- Hetzner DNS API token rotated without a `faas secrets seal`
  re-run — see `FaasTLSCertExpiryPage.md` for the rotation steps.
- The apps zone no longer delegates to this box's IP — check with
  `dig +short <zone>` and `dig +short <zone> NS`.

### kind=ondemand

Customer-owned custom domains. Each customer gets a fresh cert on
their first request. Failure modes:

- The customer's CNAME no longer points to the gatewayd IP — verify
  `dig +short CNAME <customer-domain>` returns the apps-domain
  target.
- The customer's HTTP-01 challenge endpoint (`/.well-known/acme-challenge/`)
  is being intercepted by their own CDN/proxy — verify by curl from
  outside the box.
- The customer removed their `custom_domains` row in apid but kept
  the DNS record — gatewayd's allowlist will reject the mint.
  Reach out to the customer; this is a legitimate revoke.

## Verify

```bash
# Is the refresher alive at all? Frozen = stale alerts.
curl -fsS http://127.0.0.1:9090/metrics | grep gateway_tls_cert_expiry_refresher_walk_complete_total
# Has any tick been partial in the last hour?
# (the FaasTLSCertExpiryRefresherPartial alert covers this — if
# it fires alongside, the gauge is stale.)

# What's the current allowlist state for an on-demand host?
psql -U faas -d faas -c "SELECT hostname, verified_at FROM custom_domains WHERE hostname='$HOST';"
```

## Recover

For `kind=wildcard`:

```bash
faas cert refresh --host='*.<zone>'   # forces DNS-01 re-mint
journalctl -u faas-gatewayd -f        # watch for "renewing" log line
```

For `kind=ondemand`:

```bash
# Trigger a fresh mint by hitting the cert-mint path directly.
curl -fsS https://$HOST/healthz   # this is what gatewayd's allowlist hooks
faas cert refresh --host=$HOST    # explicit refresh for diagnostic logging
```

If certmagic's renew loop is wedged (the same Warn line repeating
for > 30 minutes):

```bash
systemctl restart faas-gatewayd
# Watch the gauge climb back above 60d within 10 minutes of the
# first mint after restart.
```

## Silence

```bash
amtool silence add \
  --matchers='alertname=FaasTLSCertExpiryPageByHost,hostname=foo.example.com' \
  --duration=1h \
  --comment='customer decommissioning custom domain'
```

## Notes

This rule complements (does not replace) the aggregate
`FaasTLSCertExpiryPage`. Operators should expect the per-host rule
to fire first and with more context; the aggregate catches the
"smallest remaining" case but loses the per-host attribution. Both
fire from the same gauge family; the per-host variant is the
actionable signal.
