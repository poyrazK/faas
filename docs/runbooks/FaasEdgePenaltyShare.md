# FaasEdgePenaltyShare

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`, group
`faas_edge_security` (ADR-20261024082000 amendment 1).
Alerts: `FaasEdgePenaltyShareFailing`, `FaasEdgePenaltyShareDropped`,
`FaasEdgePenaltyShareSlow`.
Metrics: `gateway_edge_penalty_share_total{op,outcome}`,
`gateway_edge_penalty_share_dropped_total`,
`gateway_edge_penalty_share_seconds{stage}`, `gateway_edge_penalty_total{app,outcome}`.
Design: ADR-20261022105846 and its amendment 1.
Severity: warn.

## Symptom

A rule penalty boxes a client: every request from it to the app is refused
for a while. Each gateway writes the boxes it makes to the
`edge_penalty_boxes` table every 250 ms (`flush`), notifies
`edge_penalty_box`, and reads new boxes on each notification and every 15 s
(`sync`). A box that is not shared still holds on the gateway that made it.

- **Failing**: a gateway cannot write (`op="flush"`), read (`op="sync"`) or
  prune (`op="prune"`) boxes. A failed flush is not retried.
- **Dropped**: more than 10,000 boxes waited for one flush; the rest were not
  shared.
- **Slow**: boxes take more than 1 s to reach a gateway at p95 (criterion
  P2). That is the 15 s repair tick doing the work: the gateway is missing
  notifications.

## Verify

```promql
sum by (instance, op, outcome) (rate(gateway_edge_penalty_share_total[5m]))
histogram_quantile(0.95, sum by (instance, stage, le) (rate(gateway_edge_penalty_share_seconds_bucket[15m])))
sum by (app) (rate(gateway_edge_penalty_total{outcome="boxed"}[5m]))
```

```bash
journalctl -u faas-gatewayd-internal --since '-30m' --no-pager | grep -i 'penalty box'
```

## Check

- **Failing flush or sync**: Postgres reachability and the gateway's pool
  (`FaasDatabase*` alerts, pool metrics). A permission error means the runtime
  role lost row access to `edge_penalty_boxes`.
- **Failing prune**: harmless for a while; expired rows stay and are filtered
  out on read. Fix with the flush and sync causes above.
- **Dropped**: either a flood is boxing many clients on one app
  (`gateway_edge_penalty_total{outcome="boxed"}` names it), or slow writes
  delay each flush (up to 5 s) while boxes keep arriving.
- **Slow**: the gateway's `LISTEN` connection. A log line
  `penalty box subscription failed` at start means it is sharing every 15 s
  only.

## Silence

```bash
amtool silence add --matchers='alertname=~"FaasEdgePenaltyShare.*"' --duration=2h \
  --comment='penalty box sharing under investigation'
```

## Recover

1. Restore the database path or the role's grants; the next flush and sync
   resume sharing. Boxes made during the outage are not re-sent; they keep
   holding on the gateway that made them until they end.
2. Restart the gateway to re-establish a lost subscription.
3. If boxes are refusing clients in error, withdraw penalties for everyone
   with `gregalectl flags disable --key edge_penalty_box --reason "..." --yes`
   (boxes stop applying at once; the rules keep their penalty settings).
