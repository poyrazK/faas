# FaasEdgeRuleLoadFailing

Alert: `FaasEdgeRuleLoadFailing` (warn, family `edge_rules`), in
`deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metric: `gateway_edge_rule_load_total{outcome}`, which counts each read of a
host's edge-rule set from Postgres by gatewayd-internal.
Decision: ADR-091 amendment (2026-10-26).

## What it means

gatewayd-internal reads a host's rules on a cache miss (30 s TTL per host
and app, or after a rule edit drops the host's set). The alert fires when
reads fail:

- **`last_known`**: the read failed and the gateway serves the host's last
  compiled rules. Traffic flows and rules are enforced, but rule edits made
  since do not apply. The gateway retries each host every 2 s.
- **`failed`**: the read failed and the gateway holds no compiled rules for
  the host, because it restarted during the outage or evicted the host's
  set. It cannot tell which rules apply, so it refuses the host's requests
  with `503 capacity_unavailable` ("Edge security unavailable") before any
  rule kind runs. The refusals show as
  `gateway_edge_rejections_total{kind="policy_unavailable"}` and
  `gateway_edge_rule_apply_total{kind="policy",result="error"}`.

## Triage

1. Is Postgres reachable from the gateway? A failed read is almost always a
   database or pgbouncer outage, or connection exhaustion:

   ```promql
   sum by (instance, outcome) (rate(gateway_edge_rule_load_total[5m]))
   ```

   If every gateway shows failures, follow the Postgres runbooks (primary
   down, pool at its cap). If one gateway does, check its connectivity and
   its logs: `edge rule load failed; serving last known rules` for
   `last_known`, and `edge rule loader failed` for hosts it holds nothing for.
2. How many requests are refused?

   ```promql
   sum by (app) (rate(gateway_edge_rejections_total{kind="policy_unavailable"}[5m]))
   ```

## Mitigation

Restore Postgres connectivity. Gateways recover by themselves: the next read
after the 2 s backoff fills the cache. Do not restart gatewayd-internal
during a database outage. A restart drops every last-known set, turning
`last_known` hosts into `failed` ones.
