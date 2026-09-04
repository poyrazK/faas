# FaasUpstreamProbeOutcomeDegraded

Source: `pkg/promqlrules/data_placement.yaml`.
Metric: `meterd_data_upstream_probes_total{outcome}` — fleet
non-ok ratio.
Spec: §12 (probe health SLO; see ADR-098 §9.A for the canonical
outcome vocabulary).
ADR: ADR-098 §9.A (closed outcome vocabulary) + §11 (host hash
redaction).
Issue: #955.
Severity: page.

This alert is the fleet-wide complement to the existing
`FaasUpstreamProbeHighFailureRate` (severity warn, 5 % over 10m
for the timeout/refused subvocab only) — page tier above 50 %
non-ok over 5m across the FULL outcome vocabulary. The two sit
on either side of the same incident class: warn catches a
network blip, page catches fleet-wide chooser degradation
where > 50 % of the probes are returning non-ok and the
legacy-fallthrough is about to route customer traffic to a
bad upstream.

## Symptom

More than 50 % of meterd's data-placement probe outcomes are
non-`ok` for 5 minutes. The chooser bias (ADR-098 C6) is
losing signal on at least half the probes; the legacy
fallthrough is carrying customer traffic to a degraded
upstream.

## Verify

```bash
# Confirm the alert expression is the one firing.
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=sum(rate(meterd_data_upstream_probes_total{outcome!="ok"}[5m]))/sum(rate(meterd_data_upstream_probes_total[5m]))'

# Direct underlying metric — see the outcome breakdown.
curl -fsS http://127.0.0.1:9091/metrics | grep 'meterd_data_upstream_probes_total'
```

## Check

```bash
# Per-(kind, region) split — breaks down which outcome is dominating.
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=sum by (outcome) (rate(meterd_data_upstream_probes_total[5m]))'

# p95 RTT — is the degradation also connectivity-bound?
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=histogram_quantile(0.95, sum by (le, kind, region) (rate(meterd_data_upstream_rtt_ms_bucket[10m])))'

# Recent meterd slog.
journalctl -u meterd --since '-15m' --no-pager | grep -iE 'probe|outbound|tls|connection refused|timeout'
```

### Outcome → likely-cause table

| outcome      | Likely root cause | Operator action |
|--------------|-------------------|-----------------|
| `timeout`    | Inter-region network flap, customer-firewall drop | `nslookup` + `traceroute` to the upstream; check peering routes |
| `refused`    | Upstream service down | Check upstream health on the customer's side |
| `tls_handshake` | Cert chain rotation, customer CA change | Read the customer-side cert logs first |
| `dns`        | Resolver issue | `dig` + `systemd-resolved --status` |
| `unreachable`| Network partition | Cross-reference `FaasUpstreamRttDegraded` for the same window |

## Silence

```bash
amtool silence add \
  --matchers='{alertname="FaasUpstreamProbeOutcomeDegraded"}' \
  --duration=1h \
  --comment='Customer-side upstream outage confirmed; legacy fallthrough in use'
```

The 1-hour silence matches the canonical customer-side outage
window. The chooser will re-evaluate when the silence expires;
if the upstream is still degraded, the alert re-pages.

## Recover

### Path A — Network flap

```bash
# Check the vmmd region↔upstream-region routes.
ip route get <upstream-host>
# If a peer BGP session is down, restart.
systemctl restart bird
# Verify the alert clears within the next 5m window.
```

### Path B — Customer-side upstream outage

The chooser falls through to legacy defaults (ADR-098 C6). Customer
traffic continues to wake on the unaffected upstreams; the affected
region is in fallback mode. No operator action unless the outage
extends past 24 h — at that point the upstream's region eligibility
needs an admin-side decision (region retirement or pinning).

### Path C — Stalled meterd poller

```bash
systemctl status meterd
journalctl -u meterd --since '-30m' --no-pager | grep -iE 'panic|deadlock|goroutine'
# If the poller is hung, bounce it.
systemctl restart meterd
```

## Reference

- Issue #955 — closure of the missing probe-outcome alerts
- ADR-098 §9.A — closed outcome vocabulary `{ok, timeout, refused, tls_handshake, dns, unreachable}` + chooser bias
- ADR-098 §11 — host_redacted_hash redaction (label contract)
- §12 SLO list — probe health sub-row
- `FaasUpstreamProbeHighFailureRate` — sibling (warn, timeout/refused subvocab only)
- `FaasUpstreamRttDegraded` — per-(kind, region) localization