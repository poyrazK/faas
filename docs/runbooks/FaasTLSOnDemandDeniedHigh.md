# FaasTLSOnDemandDeniedHigh

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metric: `gateway_tls_on_demand_denied_total{reason}` (gatewayd-public `/metrics`).
Spec: §11 abuse-vector observability + ADR-024 H3 (closed in PR #345).
Severity: warn.

> **Legacy daemon only (revised 2026-08-04).** This runbook applies
> to the legacy `cmd/gatewayd/` daemon and to `cmd/gatewayd-public/`
> *before* PR #633. The production deployment terminates TLS at
> Caddy + Cloudflare upstream of `api.gregale.dev`, not in the
> daemon. PR #633 stripped certmagic + Hetzner DNS-01 from
> `gatewayd-public`; the legacy daemon keeps them during the
> migration window. PR-C sweeps the certmagic packages and
> this runbook will be archived alongside them.

## Symptom

On-demand cert mint denials exceed 1/min over the rolling 5-minute
window. The `reason` label set is `{allowlist, dns01, token}`;
today only `reason="allowlist"` is wire-incremented (the H3.b
follow-up bridges the certmagic ACME-issuer logger for `dns01`
and `token` — a frozen-zero on those two is the H3.b visibility
signal).

The most likely cause is **external scanning**: an attacker or a
benign misconfigured probe is hitting :443 with an SNI that isn't
in the `custom_domains` allowlist. certmagic's DecisionFunc
denies the mint, the counter bumps, and this alert fires.

## Verify

```bash
curl -fsS http://127.0.0.1:9090/metrics | grep gateway_tls_on_demand_denied_total
# Reason breakdown — the per-reason sum lets you confirm allowlist is
# the source (vs a frozen-zero on dns01 or token = H3.b unmerged).
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=sum by (reason) (rate(gateway_tls_on_demand_denied_total[5m]))'
journalctl -u faas-gatewayd-public --since '-15m' --no-pager | grep -iE 'on-demand denied|allowlist'
```

## Check

```bash
# Who is hitting us? The slog line carries the host.
journalctl -u faas-gatewayd-public --since '-1h' --no-pager \
  | grep 'on-demand denied' \
  | awk '{for(i=1;i<=NF;i++) if($i=="host=") print $(i+1)}' \
  | sort | uniq -c | sort -rn | head -20
```

If the count is dominated by a single SNI, it could be:

- **A misconfigured customer probe**: cron-job health-checks with
  the wrong Host header. Fix: ask the customer to add the SNI to
  their `custom_domains` row.
- **A scanner** (Shodan, Censys): nothing to do; the deny is the
  correct response. The alert confirms the spec §11 invariant
  holds.
- **A misconfigured upstream load balancer** in front of the
  reference node: an LB sending default SNI to :443 will trigger the deny
  on every request. Fix: pin the LB to forward the SNI correctly,
  or add the LB's SNI to the allowlist.

> **Note:** the original monolithic edge daemon role for this deny check was split by
> ADR-070; the production check now lives on `gatewayd-public`. An
> attacker SNI not in the allowlist is rejected at
> `gatewayd-public`'s certmagic surface.

## Silence

```bash
amtool silence add \
  --matchers='alertname=FaasTLSOnDemandDeniedHigh' \
  --duration=1h \
  --comment='scanner investigation in progress'
```

## Recover

Allowlist denials are the **correct** response to a non-owned SNI
— the alert is operational visibility, not a fault condition. If
the rate is sustained and the source is a known scanner, the
recovery is "let it fire and let alertmanager notify once per
shift". If a customer probe is the cause, the fix is upstream
(not in gatewayd-public).
