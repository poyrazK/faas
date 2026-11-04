# FaasEdgeWAFInline

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`, group
`faas_edge_security` (ADR-20261024082000 amendment 1).
Alerts: `FaasEdgeWAFChecksSkipped`, `FaasEdgeWAFCheckSlow`,
`FaasEdgeWAFCheckErrors`.
Metrics: `gateway_waf_inline_checks_total{app,outcome}`,
`gateway_waf_inline_skips_total{app,reason}`, `gateway_waf_inline_seconds`.
Design: ADR-831 amendments 4, 10, 12 and 16.
Severity: warn.

## Symptom

`kind=waf` rules in `warn` or `block` mode check each matched request's
headers and URL in-path. The WAF fails open: a check that cannot run, or
fails, lets the request through without a verdict.

- **ChecksSkipped**: more than 0.1% of a gateway's checks found every check
  slot busy for the 1 ms wait (`reason="busy"`), the GA criterion W2. The node
  is short of CPU for its check load. `reason="budget"` skips are one app
  using its inline budget, as under a flood, and `reason="account_share"`
  skips are one account's checks waiting behind other accounts' while it
  held at least its share of the slots (ADR-831 amendment 16); neither fires
  this alert.
- **CheckSlow**: the p95 of `gateway_waf_inline_seconds`, which includes the
  slot wait, is above 2 ms (criterion W1). Every request a warn or block rule
  matches pays it.
- **CheckErrors**: checks are failing (`outcome="error"`), so those requests
  are not protected.

## Verify

```promql
# Busy-skip share per gateway (alert threshold 0.001)
sum by (instance) (rate(gateway_waf_inline_skips_total{reason="busy"}[10m]))
  / sum by (instance) (rate(gateway_waf_inline_checks_total[10m]))

# p95 check time per gateway
histogram_quantile(0.95, sum by (instance, le) (rate(gateway_waf_inline_seconds_bucket[10m])))

# Which apps carry the load
topk(10, sum by (app) (rate(gateway_waf_inline_checks_total[10m])))
```

```bash
# Slot count and wait the gateway armed at start (ADR-831 amendment 12)
journalctl -u faas-gatewayd-internal --since '-1d' --no-pager | grep -i 'in-path checks armed'
```

## Check

- **One app dominates the checks**: a flood on one app. Its `budget` skips
  rise first; `account_share` skips mean its account's apps together hold
  the node's slots, and other accounts are being served first. A throttle
  rule or the pre-auth limit on that app is the defence, not the WAF.
- **Load is spread and the node is busy**: compare CPU on the node
  (`node_cpu_seconds_total`) with the check rate. The slot count follows
  `GOMAXPROCS` (one per processor but one, 2 to 16); a node with a CPU quota
  or fewer cores gets fewer slots.
- **Slow but not skipping**: other work on the node is lengthening checks.
  To try another sizing, `TestInlineLoad` in `pkg/edgewaf` takes
  `FAAS_WAF_INLINE_SLOTS` and `FAAS_WAF_INLINE_WAIT_US`; the gateway itself
  has no such override.
- **Errors**: `journalctl -u faas-gatewayd-internal | grep -i waf` shows the
  engine error. An error on every check after a deploy points at the rule set
  shipped with that release.

## Silence

```bash
amtool silence add --matchers='alertname=~"FaasEdgeWAF.*"' --duration=2h \
  --comment='investigating in-path WAF capacity'
```

## Recover

1. Stop the flood if there is one (throttle rule, pre-auth limit, or attack
   mode on that app).
2. Move load or add capacity on the node; the ADR-831 amendment 12 model
   (`pkg/edgewaf/slot_model_test.go`) shows the skip rate a slot count gives
   at a request rate.
3. As a last resort, stop in-path checks on the node with
   `FAAS_EDGE_WAF_INLINE_DISABLED=1` (warn and block rules only observe), or
   withdraw blocking for everyone with
   `gregalectl flags disable --key edge_waf_blocking --reason "..." --yes`
   (warn and block rules then act as observe).
4. After a capacity change, rerun `TestEdgeWAFInlineLoad_E2E` with
   `FAAS_E2E_WAF_LOAD_GATE=1` on the reference node and record the result in
   `docs/STATUS.md`.
