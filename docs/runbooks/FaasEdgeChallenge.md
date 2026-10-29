# FaasEdgeChallenge

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`, group
`faas_edge_security` (ADR-20261024082000 amendment 1).
Alerts: `FaasEdgeChallengeSolveRateLow`, `FaasEdgeChallengeSolutionsRefused`,
`FaasEdgeChallengeKeyMismatch`.
Metric: `gateway_edge_challenge_total{app,outcome}` (issued, solved, passed,
bypassed, rejected, invalid, expired),
`gateway_edge_challenge_raised_total{app}` and
`gateway_edge_challenge_key_info{key_id,source}`.
Design: ADR-831 amendments 7, 8 and 17.
Severity: warn.

## Symptom

- **SolveRateLow**: across the fleet, fewer than half the challenge pages
  served in the last hour were solved, with at least 3 pages a minute. GA
  criterion H5 expects 90% over 14 days.
- **SolutionsRefused**: one gateway refuses more than half the solutions it
  receives as `invalid` or `expired`.
- **KeyMismatch**: the gateways report more than one challenge key. A pass one
  of them issues is refused by the others, and gatewayd-public sends a
  visitor's requests to any of them, so visitors are challenged again and
  again. It fires before any challenge is served, as soon as two gateways run
  with different keys.

## Verify

```promql
# Solve ratio per app over the last hour
sum by (app) (rate(gateway_edge_challenge_total{outcome="solved"}[1h]))
  / sum by (app) (rate(gateway_edge_challenge_total{outcome="issued"}[1h]))

# Pages served per app: is one app flooded?
topk(5, sum by (app) (rate(gateway_edge_challenge_total{outcome="issued"}[1h])))

# Refusals per gateway
sum by (instance, outcome) (rate(gateway_edge_challenge_total{outcome=~"solved|invalid|expired"}[15m]))

# The key each gateway signs with: one key_id, source="fleet", on every gateway
gateway_edge_challenge_key_info{job="gatewayd-internal"}
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
  gateways (`KeyMismatch` fires too).
- **Key mismatch**: a gateway with `source="session"` signs with its own
  host's session key, which `gregalectl secrets init` makes per host, so two
  such gateways never agree. A gateway with `source="fleet"` but its own
  `key_id` was given another `gatewayd_edge_challenge_key`.

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
3. Clock drift on one gateway: fix the clock, then restart that gateway.
   Visitors sent back to the challenge solve it again.
4. Key mismatch: put one key in the fleet vault as
   `gatewayd_edge_challenge_key` (`openssl rand -hex 32`) and run the
   `gatewayd_internal_service` role on every gateway host with
   `gatewayd_edge_challenge_key_required=true`. The role installs it as
   `FAAS_EDGE_CHALLENGE_KEY_FILE` and restarts gatewayd-internal; every
   gateway then reports the same `key_id` with `source="fleet"`. Passes signed
   with an old key are refused once, and their visitors solve the challenge
   again.
