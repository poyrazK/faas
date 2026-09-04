# FaasUpstreamProbeOutcomeSilent

Source: `pkg/promqlrules/data_placement.yaml`.
Metric: `meterd_data_upstream_probes_total{outcome="ok"}` — zero
ok-probes over 30m on a (kind, region) pair.
Spec: §12 (probe health SLO; see ADR-098 §9.A for the canonical
outcome vocabulary).
ADR: ADR-098 §9.A.
Issue: #955.
Severity: info.

This alert is the SIBLING of `FaasUpstreamProbeOutcomeDegraded`
(severity page, 50 % over 5m). Page-tier catches fleet-wide
degradation; info-tier captures the silent-case where the probe
loop has stopped returning ok probes at all. The two are
mutually exclusive in steady-state — degraded is "some probes
passing, too many failing", silent is "no ok probes at all".

## Symptom

Zero ok-probes for `meterd_data_upstream_probes_total{outcome="ok"}`
on a (kind, region) pair for 30 minutes. The data-placement
probe loop has gone silent for that upstream. The chooser bias
is running on stale scores; legacy fallthrough (ADR-098 C6)
preserves service but the chooser cannot distinguish good from
bad upstreams.

## Verify

```bash
# Confirm the alert expression is the one firing.
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=sum by (kind, region) (rate(meterd_data_upstream_probes_total{outcome="ok"}[30m]))'

# Direct underlying metric — see if probes are being attempted at all.
curl -fsS http://127.0.0.1:9091/metrics | grep 'meterd_data_upstream_probes_total'
```

## Check

```bash
# Is the poller producing ANY probes? Non-ok rate alone shows
# whether the poller is alive.
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=sum by (kind, region) (rate(meterd_data_upstream_probes_total[5m]))'

# meterd poller state.
systemctl status meterd
journalctl -u meterd --since '-30m' --no-pager | grep -iE 'probe|panicked|context deadline|stale score'

# Upstream manifest — is the kind/region still registered?
gregale data-placement show --kind=<kind> --region=<region>
```

### Diagnostic decision tree

| Symptom                              | Likely root cause | Action |
|--------------------------------------|-------------------|--------|
| ok-rate 0 AND any-rate > 0           | Poller alive, all probes fail | Cross-reference `FaasUpstreamRttDegraded` |
| ok-rate 0 AND any-rate 0             | Poller stalled / manifest missing | Restart meterd; check manifest |
| ok-rate 0, kind=postgres, region=us-east | AWS peering flap | Check route + BGP |

## Silence

```bash
amtool silence add \
  --matchers='{alertname="FaasUpstreamProbeOutcomeSilent"}' \
  --duration=4h \
  --comment='Stale score window; awaiting upstream recovery'
```

The 4-hour silence matches the canonical upstream outage
window (ADR-098 C6 fallback is for transit outages < 4h).
After 4h, the chooser should re-evaluate; if the silence
expires and the condition persists, the alert re-pages.

## Recover

### Path A — Stalled meterd poller

```bash
systemctl status meterd
# If the poller is hung, bounce it.
systemctl restart meterd
# Verify the alert clears within the next 30m window.
```

### Path B — Missing upstream manifest

```bash
# Re-issue the manifest via the customer-side CLI.
gregale data-placement upsert --kind=<kind> --region=<region> \
  --upstream=<host> --tls=<ca-bundle-id>
# meterd auto-detects the new manifest via the
# 60s pg_notify subscription.
```

### Path C — AWS peering flap

```bash
# Cross-region probes typically recover on their own as
# peering re-converges. Force a chooser re-evaluation.
gregale data-placement refresh --kind=<kind> --region=<region>
```

## Reference

- Issue #955 — closure of the missing probe-outcome alerts
- ADR-098 §9.A — closed outcome vocabulary + chooser bias
- §12 SLO list — probe health sub-row
- `FaasUpstreamProbeOutcomeDegraded` — sibling (page, 50 % non-ok over 5m)
- `FaasUpstreamRttDegraded` — per-(kind, region) connectivity localization