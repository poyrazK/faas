# FaasEdgeChallenge

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`, group
`faas_edge_security` (ADR-20261024082000 amendment 1).
Alerts: `FaasEdgeChallengeSolveRateLow`, `FaasEdgeChallengeSolutionsRefused`.
Metric: `gateway_edge_challenge_total{app,outcome}` (issued, solved, passed,
bypassed, rejected, invalid, expired) and
`gateway_edge_challenge_raised_total{app}`.
Design: ADR-831 amendments 7 and 8.
Severity: warn.

## Symptom

- **SolveRateLow**: across the fleet, fewer than half the challenge pages
  served in the last hour were solved, with at least 3 pages a minute. GA
  criterion H5 expects 90% over 14 days.
- **SolutionsRefused**: one gateway refuses more than half the solutions it
  receives as `invalid` or `expired`.

## Verify

```promql
# Solve ratio per app over the last hour
sum by (app) (rate(gateway_edge_challenge_total{outcome="solved"}[1h]))
  / sum by (app) (rate(gateway_edge_challenge_total{outcome="issued"}[1h]))

# Pages served per app: is one app flooded?
topk(5, sum by (app) (rate(gateway_edge_challenge_total{outcome="issued"}[1h])))

# Refusals per gateway
sum by (instance, outcome) (rate(gateway_edge_challenge_total{outcome=~"solved|invalid|expired"}[15m]))
```

```bash
# Sampled challenge outcomes for one app, as the customer or with their
# consent (failed = a refused solution)
gregale edge-rules events --app <slug> --outcome failed --since 1h
```

## Check

- **One app has most of the pages and a low ratio**: clients that never solve
  are being challenged, which is the challenge working. Difficulty may also be
  raised (`gateway_edge_challenge_raised_total`). No action unless the
  customer reports real visitors stuck.
- **Every app's ratio dropped together**: browsers cannot solve the page. Check
  the last gatewayd-internal release for changes to the page or solver
  (`TestChallengeSolverJS` keeps the page and gateway in agreement). Visitors
  without JavaScript cannot solve it by design (ADR-831 amendment 8).
- **One gateway refuses solutions others accept**: a solution is checked by
  whichever gateway receives it, against the key and clock of the one that
  served the page. `expired` points at clock skew (check `timedatectl` and
  `FaasHostClock`); `invalid` at a challenge key that differs between
  gateways. The key derives from the session binding key, so a gateway
  started with a different binding key refuses the others' pages.

## Silence

```bash
amtool silence add --matchers='alertname=~"FaasEdgeChallenge.*"' --duration=2h \
  --comment='challenge solve rate under investigation'
```

## Recover

1. A flood on one app: nothing to recover; tell the customer the challenge is
   absorbing it, or suggest a throttle rule.
2. A solver regression: roll gatewayd-internal back to the previous release,
   or withdraw challenges while it is fixed with
   `gregalectl flags disable --key edge_challenge --reason "..." --yes`
   (challenge rules and attack mode stop challenging; nothing is deleted).
3. Key or clock drift on one gateway: fix the clock or the binding key, then
   restart that gateway. Visitors sent back to the challenge solve it again.
